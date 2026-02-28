package openai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/seyi/dagens/pkg/models"
	"github.com/seyi/dagens/pkg/secrets"
	oa "github.com/sashabaranov/go-openai"
	"golang.org/x/time/rate"
)

const (
	providerType          = "openai"
	defaultMaxRetries     = 4
	defaultRetryBaseDelay = 250 * time.Millisecond
	maxRetryBackoff       = 3 * time.Second
)

// Config controls how the OpenAI provider is instantiated.
type Config struct {
	ID             string
	Model          string
	APIKey         string
	APIKeyEnv      string
	OrganizationID string
	BaseURL        string

	// SecretProvider allows retrieving the API key securely.
	SecretProvider secrets.SecretProvider

	Parameters Parameters
	RateLimit  RateLimitConfig

	MaxRetries     int
	RetryBaseDelay time.Duration

	// HTTPClient allows custom HTTP transport tuning (important for Spark concurrency)
	HTTPClient *http.Client

	Logger *slog.Logger
}

// Parameters defines default generation parameters when a request omits them.
type Parameters struct {
	Temperature     float64
	TopP            float64
	MaxOutputTokens int
}

// RateLimitConfig configures the client-side limiter.
type RateLimitConfig struct {
	RequestsPerSecond float64
	Burst             int
}

// Provider implements models.ModelProvider for OpenAI's chat-completions API.
type Provider struct {
	*models.BaseProvider

	client  *oa.Client
	cfg     Config
	limiter *rate.Limiter
	logger  *slog.Logger
	pricing ModelPricing
}

// Ensure Provider satisfies the interface at compile time.
var _ models.ModelProvider = (*Provider)(nil)

// NewProvider creates a configured OpenAI provider instance.
func NewProvider(cfg Config) (*Provider, error) {
	if cfg.Model == "" {
		return nil, errors.New("openai provider requires a model")
	}

	apiKey := cfg.APIKey
	if apiKey == "" {
		secretName := cfg.APIKeyEnv
		if secretName == "" {
			secretName = "OPENAI_API_KEY"
		}

		// Try SecretProvider first
		if cfg.SecretProvider != nil {
			var err error
			apiKey, err = cfg.SecretProvider.GetSecret(context.Background(), secretName)
			if err != nil {
				// Log warning but continue to fallback
				slog.Debug("failed to get secret from provider, falling back to env", "secret", secretName, "error", err)
			}
		}

		// Fallback to environment variable
		if apiKey == "" {
			apiKey = os.Getenv(secretName)
		}
	}

	if apiKey == "" {
		return nil, errors.New("openai provider requires an API key (set Config.APIKey, Config.SecretProvider, or api_key_env)")
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	if cfg.ID == "" {
		cfg.ID = fmt.Sprintf("%s-%s", providerType, cfg.Model)
	}

	pricing, _ := LookupPricing(cfg.Model)

	// Use custom HTTP client or create optimized one for Spark concurrency
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Timeout: 120 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 50,
				MaxConnsPerHost:     100,
				IdleConnTimeout:     90 * time.Second,
				DisableCompression:  false,
				ForceAttemptHTTP2:   true,
			},
		}
	}

	clientConfig := oa.DefaultConfig(apiKey)
	clientConfig.HTTPClient = httpClient
	if cfg.BaseURL != "" {
		clientConfig.BaseURL = cfg.BaseURL
	}
	if cfg.OrganizationID != "" {
		clientConfig.OrgID = cfg.OrganizationID
	}

	client := oa.NewClientWithConfig(clientConfig)

	var limiter *rate.Limiter
	if cfg.RateLimit.RequestsPerSecond > 0 {
		burst := cfg.RateLimit.Burst
		if burst <= 0 {
			burst = int(math.Ceil(cfg.RateLimit.RequestsPerSecond))
		}
		limiter = rate.NewLimiter(rate.Limit(cfg.RateLimit.RequestsPerSecond), burst)
	}

	base := models.NewBaseProvider(cfg.ID, providerType, cfg.Model)

	return &Provider{
		BaseProvider: base,
		client:       client,
		cfg:          cfg,
		limiter:      limiter,
		logger:       logger,
		pricing:      pricing,
	}, nil
}

// Chat executes a non-streaming completion request.
func (p *Provider) Chat(ctx context.Context, req *models.ModelRequest) (*models.ModelResponse, error) {
	if req == nil {
		return nil, errors.New("nil model request")
	}
	if len(req.Messages) == 0 {
		return nil, errors.New("at least one message is required")
	}

	start := time.Now()
	resp, err := p.invokeChatCompletion(ctx, req)
	p.RecordRequest(time.Since(start), err)
	if err != nil {
		p.logger.Error("openai chat request failed", "error", err, "model", p.cfg.Model)
		return nil, err
	}

	if len(resp.Choices) == 0 {
		return nil, errors.New("openai returned no choices")
	}

	choice := resp.Choices[0]
	message := fromOpenAIMessage(choice.Message)
	usage := models.TokenUsage{
		PromptTokens:     resp.Usage.PromptTokens,
		CompletionTokens: resp.Usage.CompletionTokens,
		TotalTokens:      resp.Usage.TotalTokens,
	}
	// Check for cached tokens to apply correct pricing
	if resp.Usage.PromptTokensDetails != nil && resp.Usage.PromptTokensDetails.CachedTokens > 0 {
		usage.EstimatedCostUSD = EstimateCostWithCache(
			p.cfg.Model,
			usage.PromptTokens,
			usage.CompletionTokens,
			resp.Usage.PromptTokensDetails.CachedTokens,
		)
	} else {
		usage.EstimatedCostUSD = EstimateCost(p.cfg.Model, usage.PromptTokens, usage.CompletionTokens)
	}

	modelResp := &models.ModelResponse{
		Message:      message,
		FinishReason: string(choice.FinishReason),
		Usage:        usage,
		ProviderID:   p.ID(),
		ModelName:    p.Name(),
		RequestID:    resp.ID,
		Latency:      time.Since(start),
		Metadata: map[string]interface{}{
			"openai_response_id": resp.ID,
			"openai_created":     resp.Created,
		},
	}

	return modelResp, nil
}

// Stream executes a streaming completion request.
func (p *Provider) Stream(ctx context.Context, req *models.ModelRequest, handler models.StreamHandler) error {
	if handler == nil {
		return errors.New("stream handler must not be nil")
	}
	if req == nil {
		return errors.New("nil model request")
	}
	if len(req.Messages) == 0 {
		return errors.New("at least one message is required")
	}

	openReq, err := p.buildChatCompletionRequest(req)
	if err != nil {
		return err
	}

	// Add StreamOptions for streaming requests
	openReq.StreamOptions = &oa.StreamOptions{
		IncludeUsage: true,
	}

	start := time.Now()
	stream, err := p.createChatCompletionStream(ctx, openReq)
	p.RecordRequest(time.Since(start), err)
	if err != nil {
		p.logger.Error("openai stream creation failed", "error", err, "model", p.cfg.Model)
		return err
	}
	defer stream.Close()

	for {
		chunk, recvErr := stream.Recv()
		if errors.Is(recvErr, io.EOF) {
			return nil
		}
		if recvErr != nil {
			p.logger.Error("openai stream recv failed", "error", recvErr)
			return recvErr
		}

		// Extract usage data first (often comes in a chunk with empty Choices)
		var usage *models.TokenUsage
		if chunk.Usage != nil {
			tokenUsage := models.TokenUsage{
				PromptTokens:     chunk.Usage.PromptTokens,
				CompletionTokens: chunk.Usage.CompletionTokens,
				TotalTokens:      chunk.Usage.TotalTokens,
			}
			// Check for cached tokens to apply correct pricing
			if chunk.Usage.PromptTokensDetails != nil && chunk.Usage.PromptTokensDetails.CachedTokens > 0 {
				tokenUsage.EstimatedCostUSD = EstimateCostWithCache(
					p.cfg.Model,
					tokenUsage.PromptTokens,
					tokenUsage.CompletionTokens,
					chunk.Usage.PromptTokensDetails.CachedTokens,
				)
			} else {
				tokenUsage.EstimatedCostUSD = EstimateCost(p.cfg.Model, tokenUsage.PromptTokens, tokenUsage.CompletionTokens)
			}
			usage = &tokenUsage
		}

		// Handle standard delta content
		if len(chunk.Choices) > 0 {
			delta := chunk.Choices[0].Delta
			message := fromStreamDelta(delta)

			if handlerErr := handler(&models.StreamChunk{
				Delta:        message,
				FinishReason: string(chunk.Choices[0].FinishReason),
				Usage:        usage,
			}); handlerErr != nil {
				return handlerErr
			}
		} else if usage != nil {
			// Handle usage-only chunk (common at end of stream)
			if handlerErr := handler(&models.StreamChunk{
				Delta:        models.Message{},
				FinishReason: "",
				Usage:        usage,
			}); handlerErr != nil {
				return handlerErr
			}
		}
	}
}

// SupportsTools reports whether the provider supports function/tool calls.
func (p *Provider) SupportsTools() bool {
	return true
}

// SupportsVision reports whether the configured model supports vision inputs.
func (p *Provider) SupportsVision() bool {
	return p.pricing.SupportsVision
}

// SupportsStreaming reports whether the provider supports streaming responses.
func (p *Provider) SupportsStreaming() bool {
	return true
}

// SupportsJSONMode reports whether JSON mode is supported.
func (p *Provider) SupportsJSONMode() bool {
	return p.pricing.SupportsJSONMode
}

// ContextWindow returns the model's context window if known.
func (p *Provider) ContextWindow() int {
	if p.pricing.ContextWindow > 0 {
		return p.pricing.ContextWindow
	}
	return 0
}

// MaxOutputTokens returns the configured or priced max output token count.
func (p *Provider) MaxOutputTokens() int {
	if p.pricing.MaxOutputTokens > 0 {
		return p.pricing.MaxOutputTokens
	}
	return p.cfg.Parameters.MaxOutputTokens
}

// HealthCheck verifies the API key and model accessibility.
func (p *Provider) HealthCheck(ctx context.Context) error {
	start := time.Now()
	err := p.withRetry(ctx, "health_check", func(inner context.Context) error {
		if waitErr := p.waitRateLimit(inner); waitErr != nil {
			return waitErr
		}
		_, err := p.client.GetModel(inner, p.cfg.Model)
		return err
	})
	p.RecordRequest(time.Since(start), err)

	if err != nil {
		p.BaseProvider.MarkUnavailable(err)
		return err
	}

	p.BaseProvider.MarkAvailable()
	return nil
}

// Close allows the provider to release resources. (No-op currently.)
func (p *Provider) Close() error {
	return p.BaseProvider.Close()
}

func (p *Provider) invokeChatCompletion(ctx context.Context, req *models.ModelRequest) (*oa.ChatCompletionResponse, error) {
	openReq, err := p.buildChatCompletionRequest(req)
	if err != nil {
		return nil, err
	}

	var resp *oa.ChatCompletionResponse
	err = p.withRetry(ctx, "chat_completion", func(inner context.Context) error {
		if waitErr := p.waitRateLimit(inner); waitErr != nil {
			return waitErr
		}
		r, callErr := p.client.CreateChatCompletion(inner, openReq)
		if callErr != nil {
			return callErr
		}
		resp = &r
		return nil
	})
	return resp, err
}

func (p *Provider) createChatCompletionStream(ctx context.Context, req oa.ChatCompletionRequest) (*oa.ChatCompletionStream, error) {
	var stream *oa.ChatCompletionStream
	err := p.withRetry(ctx, "chat_completion_stream", func(inner context.Context) error {
		if waitErr := p.waitRateLimit(inner); waitErr != nil {
			return waitErr
		}
		s, callErr := p.client.CreateChatCompletionStream(inner, req)
		if callErr != nil {
			return callErr
		}
		stream = s
		return nil
	})
	return stream, err
}

func (p *Provider) buildChatCompletionRequest(req *models.ModelRequest) (oa.ChatCompletionRequest, error) {
	messages, err := toOpenAIMessages(req.Messages)
	if err != nil {
		return oa.ChatCompletionRequest{}, err
	}

	tools := toOpenAITools(req.Tools)
	openReq := oa.ChatCompletionRequest{
		Model:    p.cfg.Model,
		Messages: messages,
		Stop:     req.StopSequences,
	}

	if len(tools) > 0 {
		openReq.Tools = tools
	}

	if temp, ok := p.resolveTemperature(req.Temperature); ok {
		openReq.Temperature = float32(temp)
	}
	if topP, ok := p.resolveTopP(req.TopP); ok {
		openReq.TopP = float32(topP)
	}

	if maxTokens := p.resolveMaxTokens(req.MaxOutputTokens); maxTokens > 0 {
		openReq.MaxTokens = maxTokens
	}

	if req.ResponseFormat.Type == "json_object" && p.SupportsJSONMode() {
		openReq.ResponseFormat = &oa.ChatCompletionResponseFormat{
			Type: oa.ChatCompletionResponseFormatTypeJSONObject,
		}
	}

	if freq, ok := req.ExtensionParams["frequency_penalty"].(float64); ok {
		openReq.FrequencyPenalty = float32(freq)
	}
	if pres, ok := req.ExtensionParams["presence_penalty"].(float64); ok {
		openReq.PresencePenalty = float32(pres)
	}
	if seed, ok := req.ExtensionParams["seed"].(int); ok {
		openReq.Seed = &seed
	} else if seedFloat, ok := req.ExtensionParams["seed"].(float64); ok {
		seedInt := int(seedFloat)
		openReq.Seed = &seedInt
	}

	return openReq, nil
}

func (p *Provider) resolveTemperature(requestValue float64) (float64, bool) {
	switch {
	case requestValue != 0:
		return requestValue, true
	case p.cfg.Parameters.Temperature != 0:
		return p.cfg.Parameters.Temperature, true
	default:
		return 0, false
	}
}

func (p *Provider) resolveTopP(requestValue float64) (float64, bool) {
	switch {
	case requestValue != 0:
		return requestValue, true
	case p.cfg.Parameters.TopP != 0:
		return p.cfg.Parameters.TopP, true
	default:
		return 0, false
	}
}

func (p *Provider) resolveMaxTokens(requestValue int) int {
	value := requestValue
	if value <= 0 {
		value = p.cfg.Parameters.MaxOutputTokens
	}

	if p.pricing.MaxOutputTokens > 0 {
		if value <= 0 || value > p.pricing.MaxOutputTokens {
			value = p.pricing.MaxOutputTokens
		}
	}

	return value
}

func (p *Provider) withRetry(ctx context.Context, operation string, fn func(context.Context) error) error {
	maxRetries := p.cfg.MaxRetries
	if maxRetries <= 0 {
		maxRetries = defaultMaxRetries
	}

	baseDelay := p.cfg.RetryBaseDelay
	if baseDelay <= 0 {
		baseDelay = defaultRetryBaseDelay
	}

	var attempt int
	for {
		err := fn(ctx)
		if err == nil {
			return nil
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		attempt++
		if attempt > maxRetries || !p.isRetryable(err) {
			return err
		}

		// Calculate base exponential backoff
		delay := time.Duration(math.Pow(2, float64(attempt-1))) * baseDelay
		if delay > maxRetryBackoff {
			delay = maxRetryBackoff
		}

		// Apply longer backoff for rate limits (429)
		// TODO: Extract Retry-After header from error if SDK exposes it
		var apiErr *oa.APIError
		if errors.As(err, &apiErr) && apiErr.HTTPStatusCode == 429 {
			// For rate limits, use longer backoff (minimum 1 second)
			if delay < time.Second {
				delay = time.Second
			}
			// Scale up more aggressively for 429s
			delay = delay * 2
			if delay > 10*time.Second {
				delay = 10 * time.Second
			}
		}

		p.logger.Warn("openai request retry", "operation", operation, "attempt", attempt, "delay", delay, "error", err)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (p *Provider) isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}

	var apiErr *oa.APIError
	if errors.As(err, &apiErr) {
		if apiErr.HTTPStatusCode == 429 || apiErr.HTTPStatusCode >= 500 {
			return true
		}
		return false
	}

	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary()
	}

	return false
}

func (p *Provider) waitRateLimit(ctx context.Context) error {
	if p.limiter == nil {
		return nil
	}
	return p.limiter.Wait(ctx)
}
