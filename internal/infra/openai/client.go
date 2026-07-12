// Package openai implements the OpenAI-compatible AI provider port.
package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"showmethestory/internal/ports"
)

// Config configures an OpenAI-compatible HTTP API.
type Config struct {
	BaseURL   string
	APIKey    string
	URLStrict bool
	Timeout   time.Duration

	// HTTPClient is optional. It is primarily useful for callers that need to
	// control transport behavior or for tests.
	HTTPClient *http.Client
}

// Client calls an OpenAI-compatible HTTP API.
type Client struct {
	baseURL   string
	apiKey    string
	urlStrict bool
	client    *http.Client
}

var _ ports.AIClient = (*Client)(nil)

// New creates a client with the supplied provider configuration.
func New(config Config) *Client {
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: config.Timeout}
	}
	return &Client{
		baseURL:   config.BaseURL,
		apiKey:    config.APIKey,
		urlStrict: config.URLStrict,
		client:    client,
	}
}

// HTTPError reports a non-successful response from the provider.
type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("API response error, status code: %d, response body: %s", e.StatusCode, e.Body)
}

// IsFatalError reports whether retrying err with the same provider configuration
// is unlikely to succeed. Temporary network failures and server errors remain
// retryable.
func (c *Client) IsFatalError(err error) bool {
	return IsFatalError(err)
}

// IsFatalError reports whether retrying err with the same provider configuration
// is unlikely to succeed.
func IsFatalError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}

	var httpErr *HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.StatusCode == http.StatusUnauthorized ||
			httpErr.StatusCode == http.StatusForbidden ||
			httpErr.StatusCode == http.StatusNotFound
	}

	message := err.Error()
	return strings.Contains(message, "connection refused") || strings.Contains(message, "no such host")
}

// ResolveChatCompletionsURL builds the completion endpoint from baseURL and
// URLStrict. It matches the legacy client and frontend URL resolver.
func ResolveChatCompletionsURL(baseURL string, strict bool) string {
	baseURL = strings.TrimSpace(baseURL)
	baseURL = strings.TrimSuffix(baseURL, "/")
	if baseURL == "" {
		return ""
	}
	if strings.HasSuffix(baseURL, "/chat/completions") {
		return baseURL
	}
	if strict || hasAPIVersionSegment(baseURL) {
		return baseURL + "/chat/completions"
	}
	return baseURL + "/v1/chat/completions"
}

// ResolveAPIBase returns the versioned API base corresponding to baseURL.
func ResolveAPIBase(baseURL string, strict bool) string {
	return strings.TrimSuffix(ResolveChatCompletionsURL(baseURL, strict), "/chat/completions")
}

func hasAPIVersionSegment(url string) bool {
	for _, segment := range strings.Split(url, "/") {
		if len(segment) >= 2 && segment[0] == 'v' && segment[1] >= '0' && segment[1] <= '9' {
			return true
		}
	}
	return false
}

type chatRequest struct {
	Model     string          `json:"model"`
	Messages  []ports.Message `json:"messages"`
	Stream    bool            `json:"stream,omitempty"`
	MaxTokens int             `json:"max_tokens,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message      ports.Message `json:"message"`
		FinishReason string        `json:"finish_reason"`
	} `json:"choices"`
}

// Complete makes a synchronous OpenAI-compatible chat completion request.
func (c *Client) Complete(ctx context.Context, request ports.CompletionRequest) (ports.CompletionResult, error) {
	body, err := json.Marshal(chatRequest{
		Model:     request.Model,
		Messages:  request.Messages,
		MaxTokens: request.MaxTokens,
	})
	if err != nil {
		return ports.CompletionResult{}, err
	}

	responseBody, err := c.do(ctx, http.MethodPost, ResolveChatCompletionsURL(c.baseURL, c.urlStrict), body)
	if err != nil {
		return ports.CompletionResult{}, err
	}

	var response chatResponse
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return ports.CompletionResult{}, err
	}
	if len(response.Choices) == 0 {
		return ports.CompletionResult{}, errors.New("API did not return a valid choice")
	}
	return ports.CompletionResult{
		Content:      response.Choices[0].Message.Content,
		FinishReason: response.Choices[0].FinishReason,
	}, nil
}

type streamResponse struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
}

// Stream makes an OpenAI-compatible streaming chat-completion request. onChunk
// receives each non-empty content delta in order. The returned result joins all
// received content and retains the provider's finish reason.
func (c *Client) Stream(ctx context.Context, request ports.CompletionRequest, onChunk func(string)) (ports.CompletionResult, error) {
	body, err := json.Marshal(chatRequest{
		Model:     request.Model,
		Messages:  request.Messages,
		Stream:    true,
		MaxTokens: request.MaxTokens,
	})
	if err != nil {
		return ports.CompletionResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ResolveChatCompletionsURL(c.baseURL, c.urlStrict), bytes.NewReader(body))
	if err != nil {
		return ports.CompletionResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	response, err := c.client.Do(req)
	if err != nil {
		return ports.CompletionResult{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(response.Body)
		return ports.CompletionResult{}, &HTTPError{StatusCode: response.StatusCode, Body: string(responseBody)}
	}

	var content strings.Builder
	var finishReason string
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return ports.CompletionResult{Content: content.String(), FinishReason: finishReason}, err
		}
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var chunk streamResponse
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		for _, choice := range chunk.Choices {
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
			}
			if choice.Delta.Content == "" {
				continue
			}
			content.WriteString(choice.Delta.Content)
			if onChunk != nil {
				onChunk(choice.Delta.Content)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ports.CompletionResult{Content: content.String(), FinishReason: finishReason}, ctxErr
		}
		return ports.CompletionResult{Content: content.String(), FinishReason: finishReason}, err
	}
	if err := ctx.Err(); err != nil {
		return ports.CompletionResult{Content: content.String(), FinishReason: finishReason}, err
	}
	if content.Len() == 0 {
		return ports.CompletionResult{FinishReason: finishReason}, errors.New("streaming response was empty")
	}
	return ports.CompletionResult{Content: content.String(), FinishReason: finishReason}, nil
}

// ListModels retrieves and normalizes models from /models. Compatible providers
// may return the standard {"data": [...]} shape, a model object array, or IDs.
func (c *Client) ListModels(ctx context.Context) ([]ports.ModelInfo, error) {
	body, err := c.do(ctx, http.MethodGet, ResolveAPIBase(c.baseURL, c.urlStrict)+"/models", nil)
	if err != nil {
		return nil, err
	}

	var wrapped struct {
		Data []ports.ModelInfo `json:"data"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && len(wrapped.Data) > 0 {
		return normalizeModelList(wrapped.Data), nil
	}

	var models []ports.ModelInfo
	if err := json.Unmarshal(body, &models); err == nil && len(models) > 0 {
		return normalizeModelList(models), nil
	}

	var ids []string
	if err := json.Unmarshal(body, &ids); err == nil && len(ids) > 0 {
		models = make([]ports.ModelInfo, 0, len(ids))
		for _, id := range ids {
			models = append(models, ports.ModelInfo{ID: id, Name: id})
		}
		return normalizeModelList(models), nil
	}
	return nil, fmt.Errorf("cannot parse model list response: %s", body)
}

// ModelContextWindow returns the provider-reported context window for model.
func (c *Client) ModelContextWindow(ctx context.Context, model string) (int, error) {
	if strings.TrimSpace(model) == "" {
		return 0, errors.New("model is required")
	}
	body, err := c.do(ctx, http.MethodGet, ResolveAPIBase(c.baseURL, c.urlStrict)+"/models/"+model, nil)
	if err != nil {
		return 0, err
	}
	var response struct {
		ContextWindow int `json:"context_window"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return 0, err
	}
	if response.ContextWindow <= 0 {
		return 0, errors.New("provider did not return a positive context window")
	}
	return response.ContextWindow, nil
}

func (c *Client) do(ctx context.Context, method, url string, body []byte) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	response, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, &HTTPError{StatusCode: response.StatusCode, Body: string(responseBody)}
	}
	return responseBody, nil
}

func normalizeModelList(models []ports.ModelInfo) []ports.ModelInfo {
	out := make([]ports.ModelInfo, 0, len(models))
	seen := make(map[string]bool)
	for _, model := range models {
		model.ID = strings.TrimSpace(model.ID)
		if model.ID == "" || seen[model.ID] {
			continue
		}
		if model.Name == "" {
			model.Name = model.ID
		}
		seen[model.ID] = true
		out = append(out, model)
	}
	return out
}
