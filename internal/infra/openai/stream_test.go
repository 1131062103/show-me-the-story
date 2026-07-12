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

func TestClientStreamParsesContentChunks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var request struct {
			Model     string          `json:"model"`
			Messages  []ports.Message `json:"messages"`
			Stream    bool            `json:"stream"`
			MaxTokens int             `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "story-model" || !request.Stream || request.MaxTokens != 123 || !reflect.DeepEqual(request.Messages, []ports.Message{{Role: "user", Content: "Write."}}) {
			t.Fatalf("unexpected request: %#v", request)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: ignored\n\ndata: {not-json}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"Once \"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"upon a time\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()

	var chunks []string
	result, err := New(Config{BaseURL: server.URL}).Stream(context.Background(), ports.CompletionRequest{
		Model: "story-model", Messages: []ports.Message{{Role: "user", Content: "Write."}}, MaxTokens: 123,
	}, func(chunk string) { chunks = append(chunks, chunk) })
	if err != nil {
		t.Fatal(err)
	}
	if want := (ports.CompletionResult{Content: "Once upon a time", FinishReason: "stop"}); result != want {
		t.Fatalf("result = %#v, want %#v", result, want)
	}
	if want := []string{"Once ", "upon a time"}; !reflect.DeepEqual(chunks, want) {
		t.Fatalf("chunks = %#v, want %#v", chunks, want)
	}
}

func TestClientStreamHonorsContextCancellation(t *testing.T) {
	firstChunk := make(chan struct{})
	received := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"))
		w.(http.Flusher).Flush()
		close(firstChunk)
		<-release
	}))
	defer server.Close()
	defer close(release)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan struct {
		result ports.CompletionResult
		err    error
	}, 1)
	go func() {
		result, err := New(Config{BaseURL: server.URL}).Stream(ctx, ports.CompletionRequest{Model: "story-model"}, func(string) { close(received) })
		results <- struct {
			result ports.CompletionResult
			err    error
		}{result, err}
	}()
	<-firstChunk
	<-received
	cancel()

	select {
	case got := <-results:
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("error = %v, want context cancellation", got.err)
		}
		if got.result.Content != "partial" {
			t.Fatalf("partial result = %#v", got.result)
		}
	case <-time.After(time.Second):
		t.Fatal("stream did not return after context cancellation")
	}
}
