package notificationservice

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"
)

type deliveryRepositoryFixture struct {
	mu         sync.Mutex
	items      []Delivery
	channels   []string
	completed  []string
	failed     []string
	discarded  []string
	retryAt    time.Time
	claimError error
}

func (r *deliveryRepositoryFixture) ClaimNotificationDeliveries(_ context.Context, owner string, channels []string, _ int, ttl time.Duration, now time.Time) ([]Delivery, error) {
	if r.claimError != nil {
		return nil, r.claimError
	}
	r.channels = append([]string(nil), channels...)
	result := append([]Delivery(nil), r.items...)
	for i := range result {
		result[i].LeaseOwner = owner
		result[i].LeaseExpiresAt = now.Add(ttl)
	}
	return result, nil
}

func (r *deliveryRepositoryFixture) CompleteNotificationDelivery(_ context.Context, item Delivery, _ time.Time) error {
	r.mu.Lock()
	r.completed = append(r.completed, item.ID)
	r.mu.Unlock()
	return nil
}

func (r *deliveryRepositoryFixture) FailNotificationDelivery(_ context.Context, item Delivery, _ string, discard bool, retryAt, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failed = append(r.failed, item.ID)
	r.retryAt = retryAt
	if discard {
		r.discarded = append(r.discarded, item.ID)
	}
	return nil
}

func TestDeliveryWorkerCompletesAndRetriesWithoutLeakingTransportErrors(t *testing.T) {
	base := time.Date(2026, 9, 15, 10, 0, 0, 0, time.UTC)
	repository := &deliveryRepositoryFixture{items: []Delivery{
		{ID: "email-ok", Channel: ChannelEmail, Attempts: 1},
		{ID: "socket-fail", Channel: ChannelWebSocket, Attempts: 2},
	}}
	senders := map[string]DeliverySender{
		ChannelEmail: DeliverySenderFunc(func(context.Context, Delivery) error { return nil }),
		ChannelWebSocket: DeliverySenderFunc(func(context.Context, Delivery) error {
			return errors.New("provider response containing a secret")
		}),
	}
	worker, err := NewDeliveryWorker(repository, "worker-a", senders, time.Minute, time.Second, 4, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	worker.now = func() time.Time { return base }
	count, err := worker.ProcessOnce(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if count != 2 || !reflect.DeepEqual(repository.channels, []string{ChannelEmail, ChannelWebSocket}) {
		t.Fatalf("unexpected claim: count=%d channels=%v", count, repository.channels)
	}
	if !reflect.DeepEqual(repository.completed, []string{"email-ok"}) || !reflect.DeepEqual(repository.failed, []string{"socket-fail"}) {
		t.Fatalf("unexpected transitions: completed=%v failed=%v", repository.completed, repository.failed)
	}
	if want := base.Add(10 * time.Second); !repository.retryAt.Equal(want) {
		t.Fatalf("retryAt=%s want %s", repository.retryAt, want)
	}
}

func TestDeliveryWorkerDiscardsAfterMaximumAttempts(t *testing.T) {
	repository := &deliveryRepositoryFixture{items: []Delivery{{ID: "last", Channel: ChannelEmail, Attempts: maxDeliveryTries}}}
	worker, err := NewDeliveryWorker(repository, "worker-a", map[string]DeliverySender{
		ChannelEmail: DeliverySenderFunc(func(context.Context, Delivery) error { return errors.New("down") }),
	}, time.Minute, time.Second, 1, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.ProcessOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(repository.discarded, []string{"last"}) {
		t.Fatalf("discarded=%v", repository.discarded)
	}
}

func TestNewDeliveryWorkerRejectsUnsupportedSender(t *testing.T) {
	_, err := NewDeliveryWorker(&deliveryRepositoryFixture{}, "worker", map[string]DeliverySender{
		"push": DeliverySenderFunc(func(context.Context, Delivery) error { return nil }),
	}, time.Minute, time.Second, 1, nil)
	if err == nil {
		t.Fatal("expected unsupported channel error")
	}
}
