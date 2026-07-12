// Package sse adapts application events to Server-Sent Events clients.
package sse

import (
	"sync"

	"showmethestory/internal/ports"
)

// Event is one named event delivered to an SSE client.
type Event struct {
	Name string
	Data any
}

// Broadcaster fans application events out to connected SSE clients. A slow client
// does not block event publishers; events that exceed its buffer are dropped.
type Broadcaster struct {
	mu      sync.RWMutex
	clients map[chan Event]struct{}
	closed  bool
}

var _ ports.EventPublisher = (*Broadcaster)(nil)

// NewBroadcaster creates an event broadcaster with no subscribers.
func NewBroadcaster() *Broadcaster {
	return &Broadcaster{clients: make(map[chan Event]struct{})}
}

// Publish broadcasts a named application event to every connected client.
func (b *Broadcaster) Publish(name string, data any) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if b.closed {
		return
	}
	for client := range b.clients {
		select {
		case client <- Event{Name: name, Data: data}:
		default:
		}
	}
}

func (b *Broadcaster) subscribe() (<-chan Event, func()) {
	client := make(chan Event, 64)
	b.mu.Lock()
	if b.closed {
		close(client)
	} else {
		b.clients[client] = struct{}{}
	}
	b.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			b.mu.Lock()
			if _, ok := b.clients[client]; ok {
				delete(b.clients, client)
				close(client)
			}
			b.mu.Unlock()
		})
	}
	return client, unsubscribe
}

// Close disconnects all SSE clients. Publishing after Close has no effect.
func (b *Broadcaster) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return
	}
	b.closed = true
	for client := range b.clients {
		close(client)
	}
	b.clients = make(map[chan Event]struct{})
}
