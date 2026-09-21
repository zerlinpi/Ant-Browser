package redisrt

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/zerlinpi/Ant-Browser/server/platform/realtime"
)

const defaultPrefix = "ant-browser"

var refreshPresenceScript = redis.NewScript(`
if redis.call('HGET', KEYS[1], 'connection_id') ~= ARGV[1] then
  return 0
end
redis.call('HSET', KEYS[1], 'last_seen_unix_ms', ARGV[2])
redis.call('PEXPIRE', KEYS[1], ARGV[3])
return 1
`)

var removePresenceScript = redis.NewScript(`
if redis.call('HGET', KEYS[1], 'connection_id') ~= ARGV[1] then
  return 0
end
return redis.call('DEL', KEYS[1])
`)

type Bus struct {
	client *redis.Client
	prefix string
}

func Open(ctx context.Context, rawURL string) (*Bus, error) {
	options, err := redis.ParseURL(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("parse redis configuration: %w", err)
	}
	options.DialTimeout = 5 * time.Second
	options.ReadTimeout = 5 * time.Second
	options.WriteTimeout = 5 * time.Second
	client := redis.NewClient(options)
	bus := &Bus{client: client, prefix: defaultPrefix}
	if err := bus.Ping(ctx); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("connect to redis: %w", err)
	}
	return bus, nil
}

func (b *Bus) Ping(ctx context.Context) error {
	return b.client.Ping(ctx).Err()
}

func (b *Bus) SetPresence(ctx context.Context, value realtime.DevicePresence, ttl time.Duration) error {
	key := b.presenceKey(value.DeviceID)
	values := map[string]interface{}{
		"device_id": value.DeviceID, "workspace_id": value.WorkspaceID,
		"node_id": value.NodeID, "connection_id": value.ConnectionID,
		"connected_unix_ms": value.ConnectedAt.UnixMilli(),
		"last_seen_unix_ms": value.LastSeenAt.UnixMilli(),
	}
	_, err := b.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.HSet(ctx, key, values)
		pipe.PExpire(ctx, key, ttl)
		return nil
	})
	return err
}

func (b *Bus) RefreshPresence(ctx context.Context, deviceID, connectionID string, seenAt time.Time, ttl time.Duration) error {
	result, err := refreshPresenceScript.Run(
		ctx, b.client, []string{b.presenceKey(deviceID)},
		connectionID, seenAt.UnixMilli(), ttl.Milliseconds(),
	).Int()
	if err != nil {
		return err
	}
	if result == 0 {
		return realtime.ErrPresenceNotFound
	}
	return nil
}

func (b *Bus) RemovePresence(ctx context.Context, deviceID, connectionID string) error {
	_, err := removePresenceScript.Run(ctx, b.client, []string{b.presenceKey(deviceID)}, connectionID).Int()
	return err
}

func (b *Bus) Presence(ctx context.Context, deviceID string) (realtime.DevicePresence, error) {
	values, err := b.client.HGetAll(ctx, b.presenceKey(deviceID)).Result()
	if err != nil {
		return realtime.DevicePresence{}, err
	}
	if len(values) == 0 {
		return realtime.DevicePresence{}, realtime.ErrPresenceNotFound
	}
	connectedAt, err := parseUnixMilliseconds(values["connected_unix_ms"])
	if err != nil {
		return realtime.DevicePresence{}, err
	}
	lastSeenAt, err := parseUnixMilliseconds(values["last_seen_unix_ms"])
	if err != nil {
		return realtime.DevicePresence{}, err
	}
	return realtime.DevicePresence{
		DeviceID: values["device_id"], WorkspaceID: values["workspace_id"],
		NodeID: values["node_id"], ConnectionID: values["connection_id"],
		ConnectedAt: connectedAt, LastSeenAt: lastSeenAt,
	}, nil
}

func (b *Bus) PublishCommand(ctx context.Context, deviceID string, payload []byte) error {
	return b.client.Publish(ctx, b.commandChannel(deviceID), payload).Err()
}

func (b *Bus) SubscribeCommands(ctx context.Context, handler realtime.CommandHandler) error {
	if handler == nil {
		return errors.New("command handler is required")
	}
	pattern := b.prefix + ":commands:*"
	pubsub := b.client.PSubscribe(ctx, pattern)
	if _, err := pubsub.Receive(ctx); err != nil {
		_ = pubsub.Close()
		return err
	}
	channel := pubsub.Channel(redis.WithChannelSize(256))
	go func() {
		defer pubsub.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case message, ok := <-channel:
				if !ok {
					return
				}
				deviceID := strings.TrimPrefix(message.Channel, b.prefix+":commands:")
				if deviceID != "" {
					handler(deviceID, []byte(message.Payload))
				}
			}
		}
	}()
	return nil
}

func (b *Bus) PublishNotification(ctx context.Context, value realtime.UserNotification) error {
	workspaceID := strings.TrimSpace(value.WorkspaceID)
	userID := strings.TrimSpace(value.UserID)
	if workspaceID == "" || userID == "" {
		return errors.New("notification workspace and user are required")
	}
	return b.client.Publish(ctx, b.notificationChannel(workspaceID, userID), value.Payload).Err()
}

func (b *Bus) SubscribeNotifications(ctx context.Context, handler realtime.NotificationHandler) error {
	if handler == nil {
		return errors.New("notification handler is required")
	}
	prefix := b.prefix + ":notifications:"
	pubsub := b.client.PSubscribe(ctx, prefix+"*")
	if _, err := pubsub.Receive(ctx); err != nil {
		_ = pubsub.Close()
		return err
	}
	channel := pubsub.Channel(redis.WithChannelSize(256))
	go func() {
		defer pubsub.Close()
		for {
			select {
			case <-ctx.Done():
				return
			case message, ok := <-channel:
				if !ok {
					return
				}
				parts := strings.SplitN(strings.TrimPrefix(message.Channel, prefix), ":", 2)
				if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
					handler(realtime.UserNotification{
						WorkspaceID: parts[0], UserID: parts[1], Payload: []byte(message.Payload),
					})
				}
			}
		}
	}()
	return nil
}

func (b *Bus) Close() error { return b.client.Close() }

func (b *Bus) presenceKey(deviceID string) string {
	return b.prefix + ":presence:device:" + deviceID
}

func (b *Bus) commandChannel(deviceID string) string {
	return b.prefix + ":commands:" + deviceID
}

func (b *Bus) notificationChannel(workspaceID, userID string) string {
	return b.prefix + ":notifications:" + workspaceID + ":" + userID
}

func parseUnixMilliseconds(value string) (time.Time, error) {
	milliseconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid redis presence timestamp: %w", err)
	}
	return time.UnixMilli(milliseconds).UTC(), nil
}
