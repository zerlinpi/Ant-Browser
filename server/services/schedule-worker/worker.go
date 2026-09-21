package scheduleworker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	scheduleservice "github.com/zerlinpi/Ant-Browser/server/services/schedule-service"
)

type Worker struct {
	repository   scheduleservice.SchedulerRepository
	workerID     string
	pollInterval time.Duration
	batchSize    int
	logger       *slog.Logger
}

func New(repository scheduleservice.SchedulerRepository, workerID string, pollInterval time.Duration, batchSize int, logger *slog.Logger) (*Worker, error) {
	if repository == nil || workerID == "" || pollInterval <= 0 || batchSize <= 0 || logger == nil {
		return nil, errors.New("schedule worker configuration is invalid")
	}
	return &Worker{repository: repository, workerID: workerID, pollInterval: pollInterval, batchSize: batchSize, logger: logger}, nil
}
func (w *Worker) Run(ctx context.Context) error {
	w.drain(ctx)
	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			w.drain(ctx)
		}
	}
}
func (w *Worker) drain(ctx context.Context) {
	for ctx.Err() == nil {
		items, err := w.repository.ClaimDueSchedules(ctx, w.workerID, time.Now().UTC(), w.batchSize)
		if err != nil {
			w.logger.ErrorContext(ctx, "schedule_claim_failed", "worker_id", w.workerID, "error", err)
			return
		}
		if len(items) == 0 {
			return
		}
		w.logger.InfoContext(ctx, "schedules_dispatched", "worker_id", w.workerID, "count", len(items))
	}
}
