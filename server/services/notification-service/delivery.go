package notificationservice

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	ChannelEmail     = "email"
	ChannelWebSocket = "websocket"
	maxDeliveryTries = 8
)

var ErrDeliveryLease = errors.New("notification delivery lease is no longer valid")

// Delivery is a leased outbox item. Sensitive transport credentials are never
// placed in the delivery payload; RecipientEmail is loaded from the user
// record only for the email sender.
type Delivery struct {
	ID              string                 `json:"id"`
	WorkspaceID     string                 `json:"workspaceId"`
	NotificationID  string                 `json:"notificationId"`
	RecipientUserID string                 `json:"recipientUserId"`
	RecipientEmail  string                 `json:"recipientEmail,omitempty"`
	Channel         string                 `json:"channel"`
	EventType       string                 `json:"eventType"`
	Title           string                 `json:"title"`
	Body            string                 `json:"body"`
	Payload         map[string]interface{} `json:"payload,omitempty"`
	CreatedAt       time.Time              `json:"createdAt"`
	Attempts        int                    `json:"attempts"`
	LeaseOwner      string                 `json:"-"`
	LeaseExpiresAt  time.Time              `json:"-"`
}

type DeliveryRepository interface {
	ClaimNotificationDeliveries(context.Context, string, []string, int, time.Duration, time.Time) ([]Delivery, error)
	CompleteNotificationDelivery(context.Context, Delivery, time.Time) error
	FailNotificationDelivery(context.Context, Delivery, string, bool, time.Time, time.Time) error
}

type DeliverySender interface {
	Send(context.Context, Delivery) error
}

type DeliverySenderFunc func(context.Context, Delivery) error

func (f DeliverySenderFunc) Send(ctx context.Context, delivery Delivery) error {
	return f(ctx, delivery)
}

// DeliveryWorker drains email and WebSocket outbox records with leases. It is
// safe to run on multiple worker replicas: the repository owns claim ordering
// and uses row locks to prevent concurrent delivery.
type DeliveryWorker struct {
	repository  DeliveryRepository
	workerID    string
	senders     map[string]DeliverySender
	channels    []string
	leaseTTL    time.Duration
	pollEvery   time.Duration
	parallelism int
	logger      *slog.Logger
	now         func() time.Time
}

func NewDeliveryWorker(repository DeliveryRepository, workerID string, senders map[string]DeliverySender, leaseTTL, pollEvery time.Duration, parallelism int, logger *slog.Logger) (*DeliveryWorker, error) {
	if repository == nil {
		return nil, errors.New("notification delivery repository is required")
	}
	workerID = strings.TrimSpace(workerID)
	if workerID == "" || len(workerID) > 200 {
		return nil, errors.New("notification delivery worker ID is required")
	}
	if leaseTTL < 10*time.Second || leaseTTL > 30*time.Minute {
		return nil, errors.New("notification delivery lease TTL must be between 10s and 30m")
	}
	if pollEvery < 100*time.Millisecond || pollEvery > time.Minute {
		return nil, errors.New("notification delivery poll interval must be between 100ms and 1m")
	}
	if parallelism < 1 || parallelism > 32 {
		return nil, errors.New("notification delivery parallelism must be between 1 and 32")
	}
	cleanSenders := make(map[string]DeliverySender, len(senders))
	channels := make([]string, 0, len(senders))
	for channel, sender := range senders {
		channel = strings.ToLower(strings.TrimSpace(channel))
		if channel != ChannelEmail && channel != ChannelWebSocket {
			return nil, fmt.Errorf("notification delivery channel %q is unsupported", channel)
		}
		if sender == nil {
			return nil, fmt.Errorf("notification delivery sender %q is nil", channel)
		}
		if _, duplicate := cleanSenders[channel]; duplicate {
			return nil, fmt.Errorf("notification delivery channel %q is duplicated", channel)
		}
		cleanSenders[channel] = sender
		channels = append(channels, channel)
	}
	if len(channels) == 0 {
		return nil, errors.New("at least one notification delivery sender is required")
	}
	sort.Strings(channels)
	if logger == nil {
		logger = slog.Default()
	}
	return &DeliveryWorker{
		repository: repository, workerID: workerID, senders: cleanSenders,
		channels: channels, leaseTTL: leaseTTL, pollEvery: pollEvery,
		parallelism: parallelism, logger: logger, now: time.Now,
	}, nil
}

func (w *DeliveryWorker) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.pollEvery)
	defer ticker.Stop()
	for {
		if _, err := w.ProcessOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
			w.logger.ErrorContext(ctx, "notification_delivery_cycle_failed", "worker_id", w.workerID, "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// ProcessOnce is exposed for deterministic worker tests and operational
// probes. It returns the number of claimed deliveries, including retried or
// discarded records.
func (w *DeliveryWorker) ProcessOnce(ctx context.Context) (int, error) {
	now := w.now().UTC()
	deliveries, err := w.repository.ClaimNotificationDeliveries(ctx, w.workerID, append([]string(nil), w.channels...), w.parallelism, w.leaseTTL, now)
	if err != nil {
		return 0, err
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for _, delivery := range deliveries {
		delivery := delivery
		wg.Add(1)
		go func() {
			defer wg.Done()
			if delivery.LeaseOwner != w.workerID {
				mu.Lock()
				if firstErr == nil {
					firstErr = ErrDeliveryLease
				}
				mu.Unlock()
				return
			}
			sender := w.senders[delivery.Channel]
			sendErr := sender.Send(ctx, delivery)
			finishedAt := w.now().UTC()
			var transitionErr error
			if sendErr == nil {
				transitionErr = w.repository.CompleteNotificationDelivery(ctx, delivery, finishedAt)
			} else {
				discard := delivery.Attempts >= maxDeliveryTries
				transitionErr = w.repository.FailNotificationDelivery(ctx, delivery, "delivery_failed", discard, nextDeliveryAttempt(finishedAt, delivery.Attempts), finishedAt)
				w.logger.WarnContext(ctx, "notification_delivery_failed",
					"delivery_id", delivery.ID, "notification_id", delivery.NotificationID,
					"workspace_id", delivery.WorkspaceID, "channel", delivery.Channel,
					"attempt", delivery.Attempts, "discarded", discard, "error", sendErr)
			}
			if transitionErr != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = transitionErr
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return len(deliveries), firstErr
}

func nextDeliveryAttempt(now time.Time, attempt int) time.Time {
	if attempt < 1 {
		attempt = 1
	}
	shift := attempt - 1
	if shift > 7 {
		shift = 7
	}
	delay := 5 * time.Second * time.Duration(1<<shift)
	if delay > 10*time.Minute {
		delay = 10 * time.Minute
	}
	return now.Add(delay)
}
