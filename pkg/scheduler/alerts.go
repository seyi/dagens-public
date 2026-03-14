package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

type schedulerAlertPayload struct {
	Event     string                 `json:"event"`
	Severity  string                 `json:"severity"`
	Message   string                 `json:"message"`
	Timestamp time.Time              `json:"timestamp"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

var schedulerAlertHTTPClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        32,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
	},
}

func (s *Scheduler) emitOperationalAlert(ctx context.Context, event, severity, message string, metadata map[string]interface{}) {
	if s == nil {
		return
	}
	webhookURL := strings.TrimSpace(s.config.AlertWebhookURL)
	if webhookURL == "" {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	payload := schedulerAlertPayload{
		Event:     event,
		Severity:  severity,
		Message:   message,
		Timestamp: time.Now().UTC(),
		Metadata:  metadata,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		s.logger.Warn("failed to marshal scheduler alert payload", map[string]interface{}{
			"event": event,
			"error": err.Error(),
		})
		return
	}

	alertCtx, cancel := context.WithTimeout(ctx, s.config.AlertRequestTimeout)
	defer cancel()

	maxAttempts := s.config.AlertMaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	baseRetry := s.config.AlertRetryBaseInterval
	if baseRetry <= 0 {
		baseRetry = 200 * time.Millisecond
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(alertCtx, http.MethodPost, webhookURL, bytes.NewReader(body))
		if err != nil {
			s.logger.Warn("failed to build scheduler alert request", map[string]interface{}{
				"event": event,
				"error": err.Error(),
			})
			return
		}
		req.Header.Set("Content-Type", "application/json")
		resp, doErr := schedulerAlertHTTPClient.Do(req)
		if doErr == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode < 300 {
				return
			}
			s.logger.Warn("scheduler alert webhook returned non-success status", map[string]interface{}{
				"event":       event,
				"status_code": resp.StatusCode,
				"attempt":     attempt,
				"max_attempt": maxAttempts,
			})
		} else {
			s.logger.Warn("failed to deliver scheduler alert", map[string]interface{}{
				"event":       event,
				"error":       doErr.Error(),
				"attempt":     attempt,
				"max_attempt": maxAttempts,
			})
		}

		if attempt == maxAttempts {
			return
		}

		backoff := time.Duration(float64(baseRetry) * math.Pow(2, float64(attempt-1)))
		select {
		case <-alertCtx.Done():
			return
		case <-time.After(backoff):
		}
	}
}
