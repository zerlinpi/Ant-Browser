package taskworker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/zerlinpi/Ant-Browser/server/platform/postgres"
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
)

type Queue interface {
	Claim(context.Context, string, []string, time.Duration) (taskservice.Lease, error)
	Start(context.Context, taskservice.Lease) error
	Complete(context.Context, taskservice.Lease, map[string]interface{}) error
	Fail(context.Context, taskservice.Lease, string, string, bool, time.Duration) error
}

type Handler func(context.Context, taskservice.Task) (map[string]interface{}, error)

type Failure struct {
	Code       string
	Message    string
	Retryable  bool
	RetryDelay time.Duration
}

func (f Failure) Error() string {
	if f.Message != "" {
		return f.Message
	}
	return f.Code
}

type Worker struct {
	queue        Queue
	workerID     string
	handlers     map[string]Handler
	supported    []string
	leaseTTL     time.Duration
	pollInterval time.Duration
	parallelism  int
	logger       *slog.Logger
}

func New(
	queue Queue,
	workerID string,
	handlers map[string]Handler,
	leaseTTL, pollInterval time.Duration,
	parallelism int,
	logger *slog.Logger,
) (*Worker, error) {
	if queue == nil || workerID == "" || len(handlers) == 0 || leaseTTL <= 0 || pollInterval <= 0 || parallelism <= 0 || logger == nil {
		return nil, errors.New("task worker configuration is invalid")
	}
	supported := make([]string, 0, len(handlers))
	for taskType, handler := range handlers {
		if taskType == "" || handler == nil {
			return nil, errors.New("task worker handler is invalid")
		}
		supported = append(supported, taskType)
	}
	sort.Strings(supported)
	return &Worker{
		queue: queue, workerID: workerID, handlers: handlers, supported: supported,
		leaseTTL: leaseTTL, pollInterval: pollInterval, parallelism: parallelism, logger: logger,
	}, nil
}

func (w *Worker) Run(ctx context.Context, wakeups <-chan struct{}) error {
	var group sync.WaitGroup
	for slot := 0; slot < w.parallelism; slot++ {
		group.Add(1)
		go func(slot int) {
			defer group.Done()
			w.runSlot(ctx, wakeups, slot)
		}(slot)
	}
	group.Wait()
	return ctx.Err()
}

func (w *Worker) runSlot(ctx context.Context, wakeups <-chan struct{}, slot int) {
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	w.drain(ctx, slot)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.drain(ctx, slot)
		case <-wakeups:
			w.drain(ctx, slot)
		}
	}
}

func (w *Worker) drain(ctx context.Context, slot int) {
	for ctx.Err() == nil {
		lease, err := w.queue.Claim(ctx, w.workerID, w.supported, w.leaseTTL)
		switch {
		case errors.Is(err, taskservice.ErrNoWork):
			return
		case err != nil:
			w.logger.ErrorContext(ctx, "task_claim_failed", "worker_id", w.workerID, "slot", slot, "error", err)
			return
		}
		w.process(ctx, lease, slot)
	}
}

func (w *Worker) process(ctx context.Context, lease taskservice.Lease, slot int) {
	logger := w.logger.With(
		"worker_id", w.workerID, "slot", slot, "task_id", lease.Task.ID,
		"task_type", lease.Task.TaskType, "attempt", lease.Attempt,
	)
	if err := w.queue.Start(ctx, lease); err != nil {
		logger.WarnContext(ctx, "task_start_rejected", "error", err)
		return
	}
	deadline := lease.ExpiresAt.Add(-5 * time.Second)
	if deadline.Before(time.Now()) {
		deadline = lease.ExpiresAt
	}
	taskContext, cancel := context.WithDeadline(ctx, deadline)
	taskContext = postgres.WithTenantScope(taskContext, postgres.TenantScope{WorkspaceID: lease.Task.WorkspaceID})
	result, err := executeSafely(taskContext, w.handlers[lease.Task.TaskType], lease.Task)
	cancel()
	if err == nil {
		if completeErr := w.queue.Complete(ctx, lease, result); completeErr != nil {
			logger.ErrorContext(ctx, "task_complete_failed", "error", completeErr)
			return
		}
		logger.InfoContext(ctx, "task_succeeded")
		return
	}
	failure := Failure{Code: "task_execution_failed", Message: err.Error(), Retryable: true, RetryDelay: 5 * time.Second}
	var typed Failure
	if errors.As(err, &typed) {
		failure = typed
	}
	if failErr := w.queue.Fail(ctx, lease, failure.Code, failure.Message, failure.Retryable, failure.RetryDelay); failErr != nil {
		logger.ErrorContext(ctx, "task_fail_transition_failed", "error", failErr, "execution_error", err)
		return
	}
	logger.WarnContext(ctx, "task_failed", "code", failure.Code, "retryable", failure.Retryable, "error", err)
}

func executeSafely(ctx context.Context, handler Handler, task taskservice.Task) (result map[string]interface{}, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result = nil
			err = Failure{
				Code: "handler_panic", Message: fmt.Sprintf("task handler panicked: %v", recovered),
				Retryable: true, RetryDelay: 10 * time.Second,
			}
			_ = debug.Stack()
		}
	}()
	return handler(ctx, task)
}

func SystemHealthcheckHandler(_ context.Context, task taskservice.Task) (map[string]interface{}, error) {
	return map[string]interface{}{
		"ok": true, "taskId": task.ID, "checkedAt": time.Now().UTC(),
	}, nil
}
