package ports

import "context"

// Message is one OpenAI-compatible chat message.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// CompletionRequest describes a synchronous chat-completion request.
type CompletionRequest struct {
	Model     string
	Messages  []Message
	MaxTokens int
}

// CompletionResult is the normalized result of a chat completion call.
type CompletionResult struct {
	Content      string
	FinishReason string
}

// ModelInfo identifies a model advertised by a provider.
type ModelInfo struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
}

// AIClient is the provider boundary used by application workflows.
//
// Implementations must respect ctx cancellation for every network operation.
type AIClient interface {
	Complete(context.Context, CompletionRequest) (CompletionResult, error)
	Stream(context.Context, CompletionRequest, func(string)) (CompletionResult, error)
	ListModels(context.Context) ([]ModelInfo, error)
	ModelContextWindow(context.Context, string) (int, error)
	IsFatalError(error) bool
}
