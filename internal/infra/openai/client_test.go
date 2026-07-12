package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	"showmethestory/internal/ports"
)

func TestResolveChatCompletionsURL(t *testing.T) {
	tests := []struct {
		base   string
		strict bool
		want   string
	}{
		{"https://api.z.ai/api/paas/v4", false, "https://api.z.ai/api/paas/v4/chat/completions"},
		{"https://api.z.ai/api/coding/paas/v4", true, "https://api.z.ai/api/coding/paas/v4/chat/completions"},
		{"https://api.deepseek.com", false, "https://api.deepseek.com/v1/chat/completions"},
		{"https://api.deepseek.com", true, "https://api.deepseek.com/chat/completions"},
		{"https://api.openai.com/v1", false, "https://api.openai.com/v1/chat/completions"},
		{"https://api.z.ai/api/paas/v4/chat/completions", false, "https://api.z.ai/api/paas/v4/chat/completions"},
		{"  https://api.example.com/v1/  ", false, "https://api.example.com/v1/chat/completions"},
		{"", false, ""},
	}
	for _, test := range tests {
		if got := ResolveChatCompletionsURL(test.base, test.strict); got != test.want {
			t.Errorf("ResolveChatCompletionsURL(%q, %v) = %q, want %q", test.base, test.strict, got, test.want)
		}
	}
}

func TestClientCompletePreservesOpenAIWireProtocol(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Fatalf("Content-Type = %q", got)
		}
		var request struct {
			Model     string          `json:"model"`
			Messages  []ports.Message `json:"messages"`
			MaxTokens int             `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "story-model" || request.MaxTokens != 123 || !reflect.DeepEqual(request.Messages, []ports.Message{{Role: "user", Content: "Write."}}) {
			t.Fatalf("unexpected request: %#v", request)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Once upon a time"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	client := New(Config{BaseURL: server.URL, APIKey: "test-key"})
	result, err := client.Complete(context.Background(), ports.CompletionRequest{
		Model: "story-model", Messages: []ports.Message{{Role: "user", Content: "Write."}}, MaxTokens: 123,
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := (ports.CompletionResult{Content: "Once upon a time", FinishReason: "stop"}); result != want {
		t.Fatalf("result = %#v, want %#v", result, want)
	}
}

func TestClientListModelsNormalizesCompatibleResponses(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []ports.ModelInfo
	}{
		{
			name: "standard wrapped response",
			body: `{"data":[{"id":"one"},{"id":" one "},{"id":"two","name":"Two"}]}`,
			want: []ports.ModelInfo{{ID: "one", Name: "one"}, {ID: "two", Name: "Two"}},
		},
		{
			name: "object array",
			body: `[{"id":"one"},{"id":"two","name":"Two"}]`,
			want: []ports.ModelInfo{{ID: "one", Name: "one"}, {ID: "two", Name: "Two"}},
		},
		{
			name: "id array",
			body: `["one","two"]`,
			want: []ports.ModelInfo{{ID: "one", Name: "one"}, {ID: "two", Name: "two"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/models" {
					t.Fatalf("request = %s %s", r.Method, r.URL.Path)
				}
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()

			got, err := New(Config{BaseURL: server.URL}).ListModels(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("models = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestClientModelContextWindow(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/models/story-model" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"context_window":131072}`))
	}))
	defer server.Close()

	got, err := New(Config{BaseURL: server.URL}).ModelContextWindow(context.Background(), "story-model")
	if err != nil {
		t.Fatal(err)
	}
	if got != 131072 {
		t.Fatalf("context window = %d, want 131072", got)
	}
}

func TestClientCompleteHonorsContextCancellation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := New(Config{BaseURL: server.URL}).Complete(ctx, ports.CompletionRequest{Model: "story-model"})
		result <- err
	}()
	<-started
	cancel()

	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("completion did not return after context cancellation")
	}
}

func TestIsFatalError(t *testing.T) {
	tests := []struct {
		err  error
		want bool
	}{
		{context.Canceled, true},
		{&HTTPError{StatusCode: http.StatusUnauthorized}, true},
		{&HTTPError{StatusCode: http.StatusForbidden}, true},
		{&HTTPError{StatusCode: http.StatusNotFound}, true},
		{&HTTPError{StatusCode: http.StatusTooManyRequests}, false},
		{errors.New("dial tcp: connection refused"), true},
		{errors.New("dial tcp: i/o timeout"), false},
	}
	for _, test := range tests {
		if got := IsFatalError(test.err); got != test.want {
			t.Errorf("IsFatalError(%v) = %v, want %v", test.err, got, test.want)
		}
	}
}
