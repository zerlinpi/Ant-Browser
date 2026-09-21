package taskworker

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
)

type fakeQueue struct {
	mu        sync.Mutex
	lease     taskservice.Lease
	claimed   bool
	started   bool
	completed chan struct{}
}

func (q *fakeQueue) Claim(context.Context, string, []string, time.Duration) (taskservice.Lease, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.claimed {
		return taskservice.Lease{}, taskservice.ErrNoWork
	}
	q.claimed = true
	return q.lease, nil
}

func (q *fakeQueue) Start(context.Context, taskservice.Lease) error {
	q.mu.Lock()
	q.started = true
	q.mu.Unlock()
	return nil
}

func (q *fakeQueue) Complete(context.Context, taskservice.Lease, map[string]interface{}) error {
	close(q.completed)
	return nil
}

func (q *fakeQueue) Fail(context.Context, taskservice.Lease, string, string, bool, time.Duration) error {
	return nil
}

func TestWorkerProcessesSupportedTask(t *testing.T) {
	t.Parallel()
	queue := &fakeQueue{
		lease: taskservice.Lease{
			Task:    taskservice.Task{ID: "task", TaskType: "system.healthcheck", LeaseOwner: "worker"},
			Attempt: 1, ExpiresAt: time.Now().Add(time.Minute),
		},
		completed: make(chan struct{}),
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	worker, err := New(
		queue, "worker", map[string]Handler{"system.healthcheck": SystemHealthcheckHandler},
		time.Minute, time.Hour, 1, logger,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- worker.Run(ctx, make(chan struct{})) }()
	select {
	case <-queue.completed:
		cancel()
	case <-time.After(time.Second):
		cancel()
		t.Fatal("worker did not process task")
	}
	<-done
	queue.mu.Lock()
	started := queue.started
	queue.mu.Unlock()
	if !started {
		t.Fatal("worker completed a task without transitioning it to running")
	}
}
