package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/seyi/dagens/pkg/hitl"
)

const (
	defaultHITLCallbackPath = "/api/human-callback"
	maxCallbackBodyBytes    = 1 << 20 // 1 MiB
)

type hitlCallbackRuntime struct {
	path    string
	handler http.Handler
	close   func() error
}

func newHITLCallbackRuntimeFromEnv(ctx context.Context) (*hitlCallbackRuntime, error) {
	if !envBool("HITL_CALLBACK_ENABLED", false) {
		return nil, nil
	}

	secret := strings.TrimSpace(os.Getenv("HITL_CALLBACK_SECRET"))
	if secret == "" {
		return nil, fmt.Errorf("HITL_CALLBACK_ENABLED=true requires HITL_CALLBACK_SECRET")
	}

	redisAddr := strings.TrimSpace(os.Getenv("HITL_REDIS_ADDR"))
	if redisAddr == "" {
		return nil, fmt.Errorf("HITL_CALLBACK_ENABLED=true requires HITL_REDIS_ADDR")
	}

	redisDB, err := envInt("HITL_REDIS_DB", 0)
	if err != nil {
		return nil, fmt.Errorf("invalid HITL_REDIS_DB: %w", err)
	}

	redisClient := redis.NewClient(&redis.Options{
		Addr:      redisAddr,
		Password:  os.Getenv("HITL_REDIS_PASSWORD"),
		DB:        redisDB,
		TLSConfig: redisTLSConfigFromEnv(),
	})

	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := redisClient.Ping(pingCtx).Err(); err != nil {
		_ = redisClient.Close()
		return nil, fmt.Errorf("failed to connect to HITL Redis: %w", err)
	}

	block, err := envDuration("HITL_RESUMPTION_BLOCK", 5*time.Second)
	if err != nil {
		_ = redisClient.Close()
		return nil, fmt.Errorf("invalid HITL_RESUMPTION_BLOCK: %w", err)
	}
	claimIdle, err := envDuration("HITL_RESUMPTION_CLAIM_IDLE", 30*time.Second)
	if err != nil {
		_ = redisClient.Close()
		return nil, fmt.Errorf("invalid HITL_RESUMPTION_CLAIM_IDLE: %w", err)
	}
	visibilityGrace, err := envDuration("HITL_RESUMPTION_VISIBILITY_GRACE", 5*time.Minute)
	if err != nil {
		_ = redisClient.Close()
		return nil, fmt.Errorf("invalid HITL_RESUMPTION_VISIBILITY_GRACE: %w", err)
	}

	queue, err := hitl.NewRedisResumptionQueue(redisClient, hitl.RedisQueueConfig{
		Stream:          strings.TrimSpace(envString("HITL_RESUMPTION_STREAM", "hitl:resumption")),
		Group:           strings.TrimSpace(envString("HITL_RESUMPTION_GROUP", "hitl_workers")),
		Consumer:        strings.TrimSpace(envString("HITL_RESUMPTION_CONSUMER", defaultConsumerID())),
		Block:           block,
		ClaimIdle:       claimIdle,
		VisibilityGrace: visibilityGrace,
	})
	if err != nil {
		_ = redisClient.Close()
		return nil, fmt.Errorf("failed to initialize HITL Redis Streams queue: %w", err)
	}

	idStore := hitl.NewRedisIdempotencyStore(redisClient)
	metrics := hitl.NewMetricsCollector().GetMetrics()
	handler := hitl.NewCallbackHandler(queue, idStore, nil, []byte(secret), metrics)
	path := strings.TrimSpace(envString("HITL_CALLBACK_PATH", defaultHITLCallbackPath))
	if path == "" || path[0] != '/' {
		_ = redisClient.Close()
		return nil, fmt.Errorf("HITL_CALLBACK_PATH must start with '/'")
	}

	return &hitlCallbackRuntime{
		path:    path,
		handler: newHITLCallbackHTTPHandler(handler),
		close:   redisClient.Close,
	}, nil
}

func newHITLCallbackHTTPHandler(callbackHandler *hitl.CallbackHandler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		requestID := strings.TrimSpace(r.URL.Query().Get("req"))
		signature := strings.TrimSpace(r.URL.Query().Get("sig"))
		if requestID == "" || signature == "" {
			http.Error(w, "missing req or sig query parameter", http.StatusBadRequest)
			return
		}

		tsRaw := strings.TrimSpace(r.URL.Query().Get("ts"))
		timestamp, err := strconv.ParseInt(tsRaw, 10, 64)
		if err != nil || timestamp <= 0 {
			http.Error(w, "invalid ts query parameter", http.StatusBadRequest)
			return
		}

		resp, err := decodeHumanResponse(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		callbackHandler.HandleHumanCallback(w, r, requestID, timestamp, signature, resp)
	})
}

func decodeHumanResponse(body io.ReadCloser) (*hitl.HumanResponse, error) {
	defer body.Close()
	limited := io.LimitReader(body, maxCallbackBodyBytes)
	dec := json.NewDecoder(limited)
	dec.DisallowUnknownFields()

	var resp hitl.HumanResponse
	if err := dec.Decode(&resp); err != nil {
		if err == io.EOF {
			return &hitl.HumanResponse{}, nil
		}
		return nil, fmt.Errorf("invalid callback body: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("invalid callback body: expected a single JSON object")
	}
	return &resp, nil
}

func envBool(name string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	return strings.EqualFold(raw, "true")
}

func envString(name, fallback string) string {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	return raw
}

func envInt(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	return strconv.Atoi(raw)
}

func envDuration(name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	return time.ParseDuration(raw)
}

func defaultConsumerID() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		host = "api"
	}
	return "api-" + host
}

func redisTLSConfigFromEnv() *tls.Config {
	if !envBool("HITL_REDIS_TLS_ENABLED", false) {
		return nil
	}
	return &tls.Config{MinVersion: tls.VersionTLS12}
}
