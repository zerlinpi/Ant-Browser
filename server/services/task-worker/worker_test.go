package taskworker

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	notificationservice "github.com/zerlinpi/Ant-Browser/server/services/notification-service"
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
)

type fakeNotificationPublisher struct {
	mu     sync.Mutex
	inputs []notificationservice.CreateInput
	err    error
}

func (f *fakeNotificationPublisher) Publish(_ context.Context, input notificationservice.CreateInput) (notificationservice.Notification, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.inputs = append(f.inputs, input)
	return notificationservice.Notification{ID: "notification"}, f.err
}

func (f *fakeNotificationPublisher) snapshot() []notificationservice.CreateInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]notificationservice.CreateInput(nil), f.inputs...)
}

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

func TestWorkerPublishesOnlyTerminalTaskFailures(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		failure    Failure
		attempt    int
		retryLimit int
		want       int
	}{
		{name: "non retryable", failure: Failure{Code: "invalid", Retryable: false}, attempt: 1, retryLimit: 3, want: 1},
		{name: "retry scheduled", failure: Failure{Code: "temporary", Retryable: true}, attempt: 1, retryLimit: 3, want: 0},
		{name: "retry exhausted", failure: Failure{Code: "temporary", Retryable: true}, attempt: 4, retryLimit: 3, want: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			queue := &fakeQueue{completed: make(chan struct{})}
			publisher := &fakeNotificationPublisher{}
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			handler := func(context.Context, taskservice.Task) (map[string]interface{}, error) { return nil, test.failure }
			worker, err := New(queue, "worker", map[string]Handler{"workflow.execute": handler}, time.Minute, time.Hour, 1, logger, publisher)
			if err != nil {
				t.Fatal(err)
			}
			lease := taskservice.Lease{
				Task:    taskservice.Task{ID: "task", WorkspaceID: "workspace", TaskType: "workflow.execute", RequestedBy: "user", RetryLimit: test.retryLimit, LeaseOwner: "worker"},
				Attempt: test.attempt, ExpiresAt: time.Now().Add(time.Minute),
			}
			worker.process(context.Background(), lease, 0)
			inputs := publisher.snapshot()
			if len(inputs) != test.want {
				t.Fatalf("notifications=%d want=%d", len(inputs), test.want)
			}
			if test.want == 1 && (inputs[0].EventType != "task.failed" || inputs[0].RecipientUserID != "user" || inputs[0].Payload["errorCode"] != test.failure.Code) {
				t.Fatalf("unexpected notification: %+v", inputs[0])
			}
		})
	}
}
