package sse

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestFormatUsesNamedEventDataProtocol(t *testing.T) {
	got := string(Format(Event{Name: "content_chunk", Data: map[string]any{"chapter_idx": 2, "text": "first line\nsecond line"}}))
	const want = "event: content_chunk\ndata: {\"chapter_idx\":2,\"text\":\"first line\\nsecond line\"}\n\n"
	if got != want {
		t.Fatalf("Format() = %q, want %q", got, want)
	}

	got = string(Format(Event{Data: map[string]string{"status": "ok"}}))
	const defaultWant = "event: message\ndata: {\"status\":\"ok\"}\n\n"
	if got != defaultWant {
		t.Fatalf("Format() default event = %q, want %q", got, defaultWant)
	}

	got = string(Format(Event{Name: "broken", Data: make(chan int)}))
	const errorWant = "event: broken\ndata: {\"error\":\"marshal failed\"}\n\n"
	if got != errorWant {
		t.Fatalf("Format() marshal error = %q, want %q", got, errorWant)
	}
}

func TestHandlerStreamsPublishedEventAndUnsubscribesOnCancellation(t *testing.T) {
	broadcaster := NewBroadcaster()
	handler := NewHandler(broadcaster)
	ctx, cancel := context.WithCancel(context.Background())
	request := newRequest(ctx)
	response := &streamRecorder{header: make(http.Header), wrote: make(chan struct{})}

	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(response, request)
		close(done)
	}()
	waitForSubscribers(t, broadcaster, 1)

	broadcaster.Publish("content_chunk", map[string]any{"chapter_idx": 3, "text": "prose"})
	select {
	case <-response.wrote:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not write published event")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSE handler did not stop after request cancellation")
	}
	waitForSubscribers(t, broadcaster, 0)

	if got := response.header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	if got := response.header.Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("Cache-Control = %q, want no-cache", got)
	}
	if got := response.header.Get("Connection"); got != "keep-alive" {
		t.Fatalf("Connection = %q, want keep-alive", got)
	}
	if got := response.header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if got := response.body.String(); got != "event: content_chunk\ndata: {\"chapter_idx\":3,\"text\":\"prose\"}\n\n" {
		t.Fatalf("SSE response = %q", got)
	}
}

func TestBroadcasterCloseDisconnectsSubscribers(t *testing.T) {
	broadcaster := NewBroadcaster()
	client, unsubscribe := broadcaster.subscribe()
	defer unsubscribe()
	waitForSubscribers(t, broadcaster, 1)

	broadcaster.Close()
	if _, ok := <-client; ok {
		t.Fatal("subscriber channel remains open after Close")
	}
	broadcaster.Publish("after_close", "ignored")
}

func newRequest(ctx context.Context) *http.Request {
	return (&http.Request{Method: http.MethodGet, Header: make(http.Header)}).WithContext(ctx)
}

type streamRecorder struct {
	header http.Header
	body   strings.Builder
	wrote  chan struct{}
	once   sync.Once
	mu     sync.Mutex
}

func (w *streamRecorder) Header() http.Header { return w.header }
func (w *streamRecorder) WriteHeader(int)     {}
func (w *streamRecorder) Flush()              {}
func (w *streamRecorder) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.body.Write(data)
	w.once.Do(func() { close(w.wrote) })
	return n, err
}

func waitForSubscribers(t *testing.T, broadcaster *Broadcaster, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		broadcaster.mu.RLock()
		count := len(broadcaster.clients)
		broadcaster.mu.RUnlock()
		if count == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("subscriber count did not become %d", want)
}
