package hitl

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisIdempotencyStore implements IdempotencyStore using Redis string keys.
type RedisIdempotencyStore struct {
	client *redis.Client
}

func NewRedisIdempotencyStore(client *redis.Client) *RedisIdempotencyStore {
	return &RedisIdempotencyStore{client: client}
}

func (r *RedisIdempotencyStore) Exists(key string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	val, err := r.client.Exists(ctx, key).Result()
	if err != nil {
		return false, err
	}
	return val == 1, nil
}

func (r *RedisIdempotencyStore) Set(key string, ttl time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return r.client.Set(ctx, key, "1", ttl).Err()
}

func (r *RedisIdempotencyStore) SetNX(key string, ttl time.Duration) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ok, err := r.client.SetNX(ctx, key, "1", ttl).Result()
	return ok, err
}

func (r *RedisIdempotencyStore) Delete(key string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return r.client.Del(ctx, key).Err()
}

// RedisResumptionQueue implements ResumptionQueue using Redis Streams.
// Uses a consumer group for at-least-once delivery. Ack must be called after successful processing.
type RedisResumptionQueue struct {
	client          *redis.Client
	stream          string
	group           string
	consumer        string
	blockDuration   time.Duration
	claimIdle       time.Duration
	visibilityGrace time.Duration
}

// RedisQueueConfig holds tuning parameters.
type RedisQueueConfig struct {
	Stream          string
	Group           string
	Consumer        string
	Block           time.Duration
	ClaimIdle       time.Duration
	VisibilityGrace time.Duration
}

func NewRedisResumptionQueue(client *redis.Client, cfg RedisQueueConfig) (*RedisResumptionQueue, error) {
	if cfg.Stream == "" {
		cfg.Stream = "hitl:resumption"
	}
	if cfg.Group == "" {
		cfg.Group = "hitl_workers"
	}
	if cfg.Consumer == "" {
		cfg.Consumer = fmt.Sprintf("worker-%d", time.Now().UnixNano())
	}
	if cfg.Block == 0 {
		cfg.Block = 5 * time.Second
	}
	if cfg.ClaimIdle == 0 {
		cfg.ClaimIdle = 30 * time.Second
	}
	if cfg.VisibilityGrace == 0 {
		cfg.VisibilityGrace = 5 * time.Minute
	}

	q := &RedisResumptionQueue{
		client:          client,
		stream:          cfg.Stream,
		group:           cfg.Group,
		consumer:        cfg.Consumer,
		blockDuration:   cfg.Block,
		claimIdle:       cfg.ClaimIdle,
		visibilityGrace: cfg.VisibilityGrace,
	}
	if err := q.ensureGroup(context.Background()); err != nil {
		return nil, err
	}
	return q, nil
}

func (q *RedisResumptionQueue) ensureGroup(ctx context.Context) error {
	// MKSTREAM ensures stream exists.
	err := q.client.XGroupCreateMkStream(ctx, q.stream, q.group, "$").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		return fmt.Errorf("create consumer group: %w", err)
	}
	return nil
}

func (q *RedisResumptionQueue) Enqueue(ctx context.Context, job *ResumptionJob) error {
	// Serialize job
	body, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal job: %w", err)
	}
	id, err := q.client.XAdd(ctx, &redis.XAddArgs{
		Stream: q.stream,
		Values: map[string]interface{}{"payload": body},
	}).Result()
	if err != nil {
		return err
	}
	job.JobID = id
	return nil
}

func (q *RedisResumptionQueue) Dequeue(ctx context.Context) (*ResumptionJob, error) {
	// First, claim any stale pending messages to our consumer.
	claimed, _, _ := q.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream:   q.stream,
		Group:    q.group,
		Consumer: q.consumer,
		MinIdle:  q.claimIdle,
		Start:    "0-0",
		Count:    1,
	}).Result()
	if len(claimed) > 0 {
		job, err := decodeJob(claimed[0])
		if err == nil {
			return job, nil
		}
	}

	// Otherwise, read new messages.
	res, err := q.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    q.group,
		Consumer: q.consumer,
		Streams:  []string{q.stream, ">"},
		Count:    1,
		Block:    q.blockDuration,
	}).Result()
	if err != nil {
		return nil, err
	}
	if len(res) == 0 || len(res[0].Messages) == 0 {
		return nil, redis.Nil
	}
	return decodeJob(res[0].Messages[0])
}

func (q *RedisResumptionQueue) Ack(ctx context.Context, jobID string) error {
	return q.client.XAck(ctx, q.stream, q.group, jobID).Err()
}

func decodeJob(msg redis.XMessage) (*ResumptionJob, error) {
	raw, ok := msg.Values["payload"]
	if !ok {
		return nil, fmt.Errorf("missing payload field in stream message %s", msg.ID)
	}
	var payloadBytes []byte
	switch v := raw.(type) {
	case string:
		payloadBytes = []byte(v)
	case []byte:
		payloadBytes = v
	default:
		return nil, fmt.Errorf("unexpected payload type %T", raw)
	}

	var job ResumptionJob
	if err := json.Unmarshal(payloadBytes, &job); err != nil {
		return nil, fmt.Errorf("unmarshal job: %w", err)
	}
	job.JobID = msg.ID
	return &job, nil
}
