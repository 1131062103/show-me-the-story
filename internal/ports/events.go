package ports

// EventPublisher publishes application events to interested adapters. Event names
// correspond to the named Server-Sent Events consumed by the web client.
type EventPublisher interface {
	Publish(name string, data any)
}
