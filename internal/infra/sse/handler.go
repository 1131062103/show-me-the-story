package sse

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Handler exposes a Broadcaster through the Server-Sent Events protocol.
type Handler struct {
	broadcaster *Broadcaster
}

// NewHandler creates an HTTP handler for events published by broadcaster.
func NewHandler(broadcaster *Broadcaster) *Handler {
	return &Handler{broadcaster: broadcaster}
}

// ServeHTTP streams events until the request is cancelled, the connection fails,
// or the broadcaster closes.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}
	if h.broadcaster == nil {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	client, unsubscribe := h.broadcaster.subscribe()
	defer unsubscribe()

	for {
		select {
		case event, ok := <-client:
			if !ok {
				return
			}
			if _, err := w.Write(Format(event)); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

// Format serializes an event using the event/data SSE framing used by the legacy
// endpoint. Data is JSON encoded; values that cannot be encoded use its stable
// error payload instead.
func Format(event Event) []byte {
	name := event.Name
	if name == "" {
		name = "message"
	}
	data, err := json.Marshal(event.Data)
	if err != nil {
		data = []byte(`{"error":"marshal failed"}`)
	}
	return []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", name, data))
}
