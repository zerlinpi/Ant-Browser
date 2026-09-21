package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/zerlinpi/Ant-Browser/server/platform/config"
	"github.com/zerlinpi/Ant-Browser/server/platform/natstask"
	"github.com/zerlinpi/Ant-Browser/server/platform/observability"
	"github.com/zerlinpi/Ant-Browser/server/platform/postgres"
	"github.com/zerlinpi/Ant-Browser/server/platform/realtime"
	"github.com/zerlinpi/Ant-Browser/server/platform/redisrt"
	"github.com/zerlinpi/Ant-Browser/server/platform/runtimeprobe"
	"github.com/zerlinpi/Ant-Browser/server/platform/secureenvelope"
	"github.com/zerlinpi/Ant-Browser/server/platform/smtpemail"
	"github.com/zerlinpi/Ant-Browser/server/platform/taskwake"
	notificationservice "github.com/zerlinpi/Ant-Browser/server/services/notification-service"
	scheduleworker "github.com/zerlinpi/Ant-Browser/server/services/schedule-worker"
	taskservice "github.com/zerlinpi/Ant-Browser/server/services/task-service"
	taskworker "github.com/zerlinpi/Ant-Browser/server/services/task-worker"
)

func main() {
	logger := observability.NewLogger(os.Getenv("ANT_ENV"), os.Stdout)
	if err := run(logger); err != nil {
		logger.Error("worker_stopped", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.LoadWorker()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := postgres.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer store.Close()

	presence, err := openRedis(ctx, cfg)
	if err != nil {
		return err
	}
	defer presence.Close()
	wakeBus, err := openWakeBus(cfg)
	if err != nil {
		return err
	}
	defer wakeBus.Close()
	if err := wakeBus.Ping(ctx); err != nil {
		return err
	}

	wakeups := make(chan struct{}, 1)
	if err := wakeBus.Subscribe(ctx, func(taskwake.Signal) {
		select {
		case wakeups <- struct{}{}:
		default:
		}
	}); err != nil {
		return err
	}
	tasks := taskservice.New(store, nil)
	handlers := map[string]taskworker.Handler{"system.healthcheck": taskworker.SystemHealthcheckHandler}
	supported := []string{"system.healthcheck"}
	if executable := os.Getenv("ANT_PROXY_PROBE_EXECUTABLE"); executable != "" {
		crypto, err := secureenvelope.New(os.Getenv("ANT_SECRET_MASTER_KEY"), os.Getenv("ANT_ENCRYPTION_KEY_REF"), os.Getenv("ANT_SECRET_KEY_VERSION"))
		if err != nil {
			return err
		}
		secrets, err := secureenvelope.NewProxyProvider(store, crypto)
		if err != nil {
			return err
		}
		probe, err := runtimeprobe.New(executable, os.Getenv("ANT_PROXY_RUNTIME_CONFIG"), os.Getenv("ANT_PROXY_RUNTIME_ROOT"), os.Getenv("ANT_PROXY_PROBE_TARGET"), secrets)
		if err != nil {
			return err
		}
		handler, err := taskworker.NewProxyHealthHandler(store, probe)
		if err != nil {
			return err
		}
		handlers["proxy.health_check"] = handler
		supported = append(supported, "proxy.health_check")
	}
	worker, err := taskworker.New(
		tasks, cfg.WorkerID,
		handlers,
		cfg.LeaseTTL, cfg.PollInterval, cfg.Parallelism, logger,
	)
	if err != nil {
		return err
	}
	scheduler, err := scheduleworker.New(store, cfg.WorkerID, cfg.PollInterval, cfg.Parallelism, logger)
	if err != nil {
		return err
	}
	go func() {
		if schedulerErr := scheduler.Run(ctx); schedulerErr != nil && !errors.Is(schedulerErr, context.Canceled) {
			logger.Error("schedule_worker_stopped", "error", schedulerErr)
		}
	}()
	notificationWorker, err := openNotificationWorker(cfg, store, presence, logger)
	if err != nil {
		return err
	}
	go func() {
		if notificationErr := notificationWorker.Run(ctx); notificationErr != nil && !errors.Is(notificationErr, context.Canceled) {
			logger.Error("notification_worker_stopped", "error", notificationErr)
		}
	}()
	logger.Info(
		"worker_started", "worker_id", cfg.WorkerID, "parallelism", cfg.Parallelism,
		"supported_task_types", supported,
	)
	err = worker.Run(ctx, wakeups)
	if errors.Is(err, context.Canceled) {
		logger.Info("worker_shutdown_complete", "worker_id", cfg.WorkerID)
		return nil
	}
	return err
}

func openRedis(ctx context.Context, cfg config.WorkerConfig) (realtime.Bus, error) {
	if cfg.RedisURL == "" {
		return realtime.NewMemory(), nil
	}
	return redisrt.Open(ctx, cfg.RedisURL)
}

func openWakeBus(cfg config.WorkerConfig) (taskwake.Bus, error) {
	if cfg.NATSURL == "" {
		return taskwake.NewMemory(), nil
	}
	return natstask.Open(cfg.NATSURL, "ant-browser-worker-"+cfg.WorkerID)
}

func openNotificationWorker(cfg config.WorkerConfig, store notificationservice.DeliveryRepository, bus realtime.Bus, logger *slog.Logger) (*notificationservice.DeliveryWorker, error) {
	senders := map[string]notificationservice.DeliverySender{
		notificationservice.ChannelWebSocket: notificationservice.DeliverySenderFunc(func(ctx context.Context, delivery notificationservice.Delivery) error {
			payload, err := json.Marshal(map[string]interface{}{
				"type": "notification.created",
				"data": map[string]interface{}{
					"id": delivery.NotificationID, "workspaceId": delivery.WorkspaceID,
					"eventType": delivery.EventType, "title": delivery.Title,
					"body": delivery.Body, "payload": delivery.Payload,
					"createdAt": delivery.CreatedAt,
				},
			})
			if err != nil {
				return err
			}
			return bus.PublishNotification(ctx, realtime.UserNotification{
				WorkspaceID: delivery.WorkspaceID, UserID: delivery.RecipientUserID, Payload: payload,
			})
		}),
	}
	if cfg.SMTPAddress != "" {
		email, err := smtpemail.New(smtpemail.Config{
			Address: cfg.SMTPAddress, Username: cfg.SMTPUsername,
			Password: cfg.SMTPPassword, From: cfg.SMTPFrom, TLSMode: cfg.SMTPTLSMode,
		})
		if err != nil {
			return nil, err
		}
		senders[notificationservice.ChannelEmail] = notificationservice.DeliverySenderFunc(func(ctx context.Context, delivery notificationservice.Delivery) error {
			return email.Send(ctx, delivery.RecipientEmail, delivery.Title, delivery.Body)
		})
	}
	parallelism := cfg.Parallelism
	if parallelism > 32 {
		parallelism = 32
	}
	return notificationservice.NewDeliveryWorker(
		store, "notification:"+cfg.WorkerID, senders,
		cfg.LeaseTTL, cfg.PollInterval, parallelism, logger,
	)
}
