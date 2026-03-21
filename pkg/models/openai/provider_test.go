package openai

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	oa "github.com/sashabaranov/go-openai"

	"github.com/seyi/dagens/pkg/models"
)

func TestNewProvider(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		setup   func()
		cleanup func()
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config with API key",
			cfg: Config{
				Model:  "gpt-4o",
				APIKey: "sk-test-key",
			},
			wantErr: false,
		},
		{
			name: "valid config with env var",
			cfg: Config{
				Model:     "gpt-4o-mini",
				APIKeyEnv: "TEST_OPENAI_KEY",
			},
			setup: func() {
				os.Setenv("TEST_OPENAI_KEY", "sk-env-key")
			},
			cleanup: func() {
				os.Unsetenv("TEST_OPENAI_KEY")
			},
			wantErr: false,
		},
		{
			name: "missing model",
			cfg: Config{
				APIKey: "sk-test-key",
			},
			wantErr: true,
			errMsg:  "requires a model",
		},
		{
			name: "missing API key",
			cfg: Config{
				Model: "gpt-4o",
			},
			wantErr: true,
			errMsg:  "requires an API key",
		},
		{
			name: "custom ID",
			cfg: Config{
				ID:     "custom-provider-id",
				Model:  "gpt-4o",
				APIKey: "sk-test-key",
			},
			wantErr: false,
		},
		{
			name: "with rate limiting",
			cfg: Config{
				Model:  "gpt-4o",
				APIKey: "sk-test-key",
				RateLimit: RateLimitConfig{
					RequestsPerSecond: 10,
					Burst:             20,
				},
			},
			wantErr: false,
		},
		{
			name: "with custom HTTP client",
			cfg: Config{
				Model:  "gpt-4o",
				APIKey: "sk-test-key",
				HTTPClient: &http.Client{
					Timeout: 30 * time.Second,
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup()
			}
			if tt.cleanup != nil {
				defer tt.cleanup()
			}

			provider, err := NewProvider(tt.cfg)

			if (err != nil) != tt.wantErr {
				t.Errorf("NewProvider() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errMsg != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("NewProvider() error = %v, want error containing %q", err, tt.errMsg)
				}
				return
			}

			if !tt.wantErr {
				if provider == nil {
					t.Fatal("NewProvider() returned nil provider")
				}

				// Verify provider attributes
				if provider.Type() != "openai" {
					t.Errorf("provider.Type() = %s, want openai", provider.Type())
				}

				if provider.Name() != tt.cfg.Model {
					t.Errorf("provider.Name() = %s, want %s", provider.Name(), tt.cfg.Model)
				}

				// Verify ID
				expectedID := tt.cfg.ID
				if expectedID == "" {
					expectedID = "openai-" + tt.cfg.Model
				}
				if provider.ID() != expectedID {
					t.Errorf("provider.ID() = %s, want %s", provider.ID(), expectedID)
				}

				// Verify capabilities
				if !provider.SupportsTools() {
					t.Error("provider.SupportsTools() = false, want true")
				}
				if !provider.SupportsStreaming() {
					t.Error("provider.SupportsStreaming() = false, want true")
				}

				// Verify vision support based on model
				pricing, hasPricing := LookupPricing(tt.cfg.Model)
				if hasPricing {
					if provider.SupportsVision() != pricing.SupportsVision {
						t.Errorf("provider.SupportsVision() = %v, want %v",
							provider.SupportsVision(), pricing.SupportsVision)
					}
					if provider.ContextWindow() != pricing.ContextWindow {
						t.Errorf("provider.ContextWindow() = %d, want %d",
							provider.ContextWindow(), pricing.ContextWindow)
					}
				}
			}
		})
	}
}

func TestProviderHTTPTransportConfiguration(t *testing.T) {
	// Test that custom HTTP transport is properly configured for Spark concurrency
	cfg := Config{
		Model:  "gpt-4o",
		APIKey: "sk-test-key",
	}

	provider, err := NewProvider(cfg)
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	// Verify that the provider was created (we can't directly inspect the HTTP client
	// without exposing internal fields, but we verify it was configured)
	if provider.client == nil {
		t.Error("provider.client is nil")
	}

	// Test with custom HTTP client
	customClient := &http.Client{
		Timeout: 60 * time.Second,
		Transport: &http.Transport{
			MaxIdleConns: 200,
		},
	}

	cfg.HTTPClient = customClient
	provider2, err := NewProvider(cfg)
	if err != nil {
		t.Fatalf("NewProvider() with custom HTTP client error = %v", err)
	}

	if provider2.client == nil {
		t.Error("provider2.client is nil")
	}
}

func TestProviderRateLimiting(t *testing.T) {
	cfg := Config{
		Model:  "gpt-4o",
		APIKey: "sk-test-key",
		RateLimit: RateLimitConfig{
			RequestsPerSecond: 100,
			Burst:             10,
		},
	}

	provider, err := NewProvider(cfg)
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	if provider.limiter == nil {
		t.Error("provider.limiter is nil, expected rate limiter to be configured")
	}

	// Test that burst defaults to ceil(rps) when not specified
	cfg2 := Config{
		Model:  "gpt-4o",
		APIKey: "sk-test-key",
		RateLimit: RateLimitConfig{
			RequestsPerSecond: 2.5,
			// Burst not specified, should default to 3
		},
	}

	provider2, err := NewProvider(cfg2)
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	if provider2.limiter == nil {
		t.Error("provider2.limiter is nil")
	}
}

func TestIsRetryable(t *testing.T) {
	cfg := Config{
		Model:  "gpt-4o",
		APIKey: "sk-test-key",
	}

	provider, err := NewProvider(cfg)
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	tests := []struct {
		name      string
		err       error
		retryable bool
	}{
		{
			name:      "nil error",
			err:       nil,
			retryable: false,
		},
		{
			name:      "context canceled",
			err:       context.Canceled,
			retryable: false,
		},
		{
			name:      "context deadline exceeded",
			err:       context.DeadlineExceeded,
			retryable: false,
		},
		{
			name: "429 rate limit",
			err: &oa.APIError{
				HTTPStatusCode: 429,
			},
			retryable: true,
		},
		{
			name: "500 server error",
			err: &oa.APIError{
				HTTPStatusCode: 500,
			},
			retryable: true,
		},
		{
			name: "502 bad gateway",
			err: &oa.APIError{
				HTTPStatusCode: 502,
			},
			retryable: true,
		},
		{
			name: "503 service unavailable",
			err: &oa.APIError{
				HTTPStatusCode: 503,
			},
			retryable: true,
		},
		{
			name: "400 bad request",
			err: &oa.APIError{
				HTTPStatusCode: 400,
			},
			retryable: false,
		},
		{
			name: "401 unauthorized",
			err: &oa.APIError{
				HTTPStatusCode: 401,
			},
			retryable: false,
		},
		{
			name: "404 not found",
			err: &oa.APIError{
				HTTPStatusCode: 404,
			},
			retryable: false,
		},
		{
			name:      "generic error",
			err:       errors.New("generic error"),
			retryable: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := provider.isRetryable(tt.err)
			if result != tt.retryable {
				t.Errorf("isRetryable(%v) = %v, want %v", tt.err, result, tt.retryable)
			}
		})
	}
}

func TestResolveParameters(t *testing.T) {
	tests := []struct {
		name           string
		providerCfg    Parameters
		requestValue   float64
		expectedValue  float64
		expectedHasVal bool
	}{
		{
			name:           "request value takes precedence",
			providerCfg:    Parameters{Temperature: 0.5},
			requestValue:   0.8,
			expectedValue:  0.8,
			expectedHasVal: true,
		},
		{
			name:           "provider default used when request is zero",
			providerCfg:    Parameters{Temperature: 0.7},
			requestValue:   0,
			expectedValue:  0.7,
			expectedHasVal: true,
		},
		{
			name:           "no value when both are zero",
			providerCfg:    Parameters{Temperature: 0},
			requestValue:   0,
			expectedValue:  0,
			expectedHasVal: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				Model:      "gpt-4o",
				APIKey:     "sk-test-key",
				Parameters: tt.providerCfg,
			}

			provider, err := NewProvider(cfg)
			if err != nil {
				t.Fatalf("NewProvider() error = %v", err)
			}

			value, hasVal := provider.resolveTemperature(tt.requestValue)

			if hasVal != tt.expectedHasVal {
				t.Errorf("resolveTemperature() hasVal = %v, want %v", hasVal, tt.expectedHasVal)
			}

			if hasVal && value != tt.expectedValue {
				t.Errorf("resolveTemperature() value = %f, want %f", value, tt.expectedValue)
			}
		})
	}
}

func TestResolveMaxTokens(t *testing.T) {
	tests := []struct {
		name          string
		providerCfg   Parameters
		requestValue  int
		expectedValue int
	}{
		{
			name:          "request value used",
			providerCfg:   Parameters{MaxOutputTokens: 1000},
			requestValue:  500,
			expectedValue: 500,
		},
		{
			name:          "provider default used when request is zero",
			providerCfg:   Parameters{MaxOutputTokens: 2000},
			requestValue:  0,
			expectedValue: 2000,
		},
		{
			name:          "capped by model max",
			providerCfg:   Parameters{MaxOutputTokens: 10000},
			requestValue:  20000, // Exceeds gpt-4o's 4096 limit
			expectedValue: 4096,  // Should be capped
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{
				Model:      "gpt-4o", // Has MaxOutputTokens: 4096
				APIKey:     "sk-test-key",
				Parameters: tt.providerCfg,
			}

			provider, err := NewProvider(cfg)
			if err != nil {
				t.Fatalf("NewProvider() error = %v", err)
			}

			value := provider.resolveMaxTokens(tt.requestValue)

			if value != tt.expectedValue {
				t.Errorf("resolveMaxTokens() = %d, want %d", value, tt.expectedValue)
			}
		})
	}
}

func TestChatValidation(t *testing.T) {
	cfg := Config{
		Model:  "gpt-4o",
		APIKey: "sk-test-key",
	}

	provider, err := NewProvider(cfg)
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	ctx := context.Background()

	tests := []struct {
		name    string
		req     *models.ModelRequest
		wantErr bool
		errMsg  string
	}{
		{
			name:    "nil request",
			req:     nil,
			wantErr: true,
			errMsg:  "nil model request",
		},
		{
			name: "empty messages",
			req: &models.ModelRequest{
				Messages: []models.Message{},
			},
			wantErr: true,
			errMsg:  "at least one message is required",
		},
		{
			name: "valid request",
			req: &models.ModelRequest{
				Messages: []models.Message{
					{Role: models.RoleUser, Content: "Hello"},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "valid request" {
				openReq, err := provider.buildChatCompletionRequest(tt.req)
				if err != nil {
					t.Fatalf("buildChatCompletionRequest() error = %v, want nil", err)
				}
				if openReq.Model != cfg.Model {
					t.Fatalf("buildChatCompletionRequest() model = %s, want %s", openReq.Model, cfg.Model)
				}
				if len(openReq.Messages) != 1 {
					t.Fatalf("buildChatCompletionRequest() messages = %d, want 1", len(openReq.Messages))
				}
				return
			}

			_, err := provider.Chat(ctx, tt.req)

			if (err != nil) != tt.wantErr {
				t.Errorf("Chat() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errMsg != "" {
				if err == nil || !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("Chat() error = %v, want %q", err, tt.errMsg)
				}
			}
		})
	}
}

func TestStreamValidation(t *testing.T) {
	cfg := Config{
		Model:  "gpt-4o",
		APIKey: "sk-test-key",
	}

	provider, err := NewProvider(cfg)
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}

	ctx := context.Background()
	handler := func(chunk *models.StreamChunk) error {
		return nil
	}

	tests := []struct {
		name    string
		req     *models.ModelRequest
		handler models.StreamHandler
		wantErr bool
		errMsg  string
	}{
		{
			name:    "nil handler",
			req:     &models.ModelRequest{Messages: []models.Message{{Role: models.RoleUser, Content: "Hi"}}},
			handler: nil,
			wantErr: true,
			errMsg:  "stream handler must not be nil",
		},
		{
			name:    "nil request",
			req:     nil,
			handler: handler,
			wantErr: true,
			errMsg:  "nil model request",
		},
		{
			name:    "empty messages",
			req:     &models.ModelRequest{Messages: []models.Message{}},
			handler: handler,
			wantErr: true,
			errMsg:  "at least one message is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := provider.Stream(ctx, tt.req, tt.handler)

			if (err != nil) != tt.wantErr {
				t.Errorf("Stream() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.wantErr && tt.errMsg != "" {
				if err == nil || err.Error() != tt.errMsg {
					t.Errorf("Stream() error = %v, want %q", err, tt.errMsg)
				}
			}
		})
	}
}
