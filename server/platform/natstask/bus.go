package natstask

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/zerlinpi/Ant-Browser/server/platform/taskwake"
)

const (
	wakeupSubject = "ant.tasks.wakeup"
	workerQueue   = "ant-task-workers"
)

type Bus struct {
	connection *nats.Conn
	closeOnce  sync.Once
}

func Open(rawURL, clientName string) (*Bus, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, errors.New("NATS URL is required")
	}
	connection, err := nats.Connect(
		rawURL,
		nats.Name(strings.TrimSpace(clientName)),
		nats.Timeout(5*time.Second),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(time.Second),
		nats.PingInterval(20*time.Second),
		nats.MaxPingsOutstanding(3),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to NATS: %w", err)
	}
	return &Bus{connection: connection}, nil
}

func (b *Bus) Ping(ctx context.Context) error {
	if !b.connection.IsConnected() {
		return errors.New("NATS is not connected")
	}
	return b.connection.FlushWithContext(ctx)
}

func (b *Bus) Notify(ctx context.Context, signal taskwake.Signal) error {
	payload, err := json.Marshal(signal)
	if err != nil {
		return err
	}
	if err := b.connection.Publish(wakeupSubject, payload); err != nil {
		return err
	}
	return b.connection.FlushWithContext(ctx)
}

func (b *Bus) Subscribe(ctx context.Context, handler taskwake.Handler) error {
	if handler == nil {
		return errors.New("task wake handler is required")
	}
	subscription, err := b.connection.QueueSubscribe(wakeupSubject, workerQueue, func(message *nats.Msg) {
		var signal taskwake.Signal
		if json.Unmarshal(message.Data, &signal) == nil && signal.TaskID != "" {
			handler(signal)
		}
	})
	if err != nil {
		return err
	}
	if err := b.connection.FlushWithContext(ctx); err != nil {
		_ = subscription.Unsubscribe()
		return err
	}
	go func() {
		<-ctx.Done()
		_ = subscription.Unsubscribe()
	}()
	return nil
}

func (b *Bus) Close() error {
	b.closeOnce.Do(func() {
		_ = b.connection.Drain()
		b.connection.Close()
	})
	return nil
}
