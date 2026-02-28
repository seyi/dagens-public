package hitl

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func withPostgres(t *testing.T, fn func(ctx context.Context, pool *pgxpool.Pool)) {
	t.Helper()
	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:15-alpine",
		postgres.WithDatabase("testdb"),
		postgres.WithUsername("testuser"),
		postgres.WithPassword("testpass"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("postgres container unavailable: %v", err)
	}
	defer testcontainers.TerminateContainer(pgContainer) //nolint:errcheck

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// Retry connection with backoff to ensure PostgreSQL is ready
	var pool *pgxpool.Pool
	for i := 0; i < 10; i++ {
		pool, err = pgxpool.New(ctx, connStr)
		if err == nil {
			// Verify connection is working
			if pingErr := pool.Ping(ctx); pingErr == nil {
				break
			}
			pool.Close()
		}
		time.Sleep(time.Duration(100*(i+1)) * time.Millisecond)
	}
	require.NoError(t, err, "failed to connect to postgres after retries")
	defer pool.Close()

	fn(ctx, pool)
}

func withRedis(t *testing.T, fn func(ctx context.Context, client *redis.Client)) {
	t.Helper()
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
	}
	redisC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Skipf("redis container unavailable: %v", err)
	}
	defer testcontainers.TerminateContainer(redisC) //nolint:errcheck

	endpoint, err := redisC.Endpoint(ctx, "")
	require.NoError(t, err)

	client := redis.NewClient(&redis.Options{Addr: endpoint})
	fn(ctx, client)
	_ = client.Close()
}

func TestPostgresCheckpointStore_RoundTrip(t *testing.T) {
	withPostgres(t, func(ctx context.Context, pool *pgxpool.Pool) {
		store, err := NewPostgresCheckpointStore(ctx, pool)
		require.NoError(t, err)

		cp := &ExecutionCheckpoint{
			RequestID:    "req-1",
			GraphID:      "graph-1",
			GraphVersion: "v1",
			NodeID:       "node-a",
			StateData:    []byte(`state`),
			CreatedAt:    time.Now(),
			ExpiresAt:    time.Now().Add(time.Hour),
		}

		require.NoError(t, store.Create(cp))

		out, err := store.GetByRequestID("req-1")
		require.NoError(t, err)
		require.Equal(t, cp.GraphID, out.GraphID)
		require.Equal(t, 0, out.FailureCount)

		updated, err := store.RecordFailure("req-1", ErrHumanTimeout)
		require.NoError(t, err)
		require.Equal(t, 1, updated.FailureCount)
		require.NotZero(t, updated.LastAttempt)

		require.NoError(t, store.MoveToCheckpointDLQ("req-1", "permanent"))
		_, err = store.GetByRequestID("req-1")
		require.ErrorIs(t, err, ErrCheckpointNotFound)
	})
}

func TestRedisStores_IdempotencyAndQueue(t *testing.T) {
	withRedis(t, func(ctx context.Context, client *redis.Client) {
		idStore := NewRedisIdempotencyStore(client)
		queue, err := NewRedisResumptionQueue(client, RedisQueueConfig{})
		require.NoError(t, err)

		ok, err := idStore.SetNX("processing:req-1", time.Minute)
		require.NoError(t, err)
		require.True(t, ok)

		ok, err = idStore.SetNX("processing:req-1", time.Minute)
		require.NoError(t, err)
		require.False(t, ok)

		job := &ResumptionJob{RequestID: "req-1", Timestamp: time.Now().Unix()}
		require.NoError(t, queue.Enqueue(ctx, job))

		out, err := queue.Dequeue(ctx)
		require.NoError(t, err)
		require.Equal(t, "req-1", out.RequestID)
		require.NoError(t, queue.Ack(ctx, out.JobID))
	})
}
