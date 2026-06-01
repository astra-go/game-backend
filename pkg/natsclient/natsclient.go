// Package natsclient provides a unified NATS client interface for the game backend.
// It wraps github.com/astra-go/astra/mq/nats for message publishing and
// github.com/nats-io/nats.go for request-response patterns.
//
// All production code should use the Client interface instead of raw *nats.Conn
// to enable testing and future backend swaps.
package natsclient

import (
	"context"
	"fmt"
	"time"

	"github.com/astra-go/astra/mq"
	astranats "github.com/astra-go/astra/mq/nats"
	"github.com/nats-io/nats.go"
)

// Client is the unified NATS client interface used throughout the game backend.
// It combines the astra mq.Producer interface (Publish) with NATS request-response.
type Client interface {
	// Publish sends a message to the given subject.
	Publish(subject string, data []byte) error
	// Request performs a synchronous request-response with a timeout.
	Request(subject string, data []byte, timeout time.Duration) (*nats.Msg, error)
	// Subscribe subscribes to a subject with a callback.
	Subscribe(subject string, cb nats.MsgHandler) (*nats.Subscription, error)
	// QueueSubscribe subscribes to a subject in a queue group.
	QueueSubscribe(subject, queue string, cb nats.MsgHandler) (*nats.Subscription, error)
	// Close closes the NATS connection.
	Close()
	// Raw returns the underlying *nats.Conn for advanced usage.
	// Prefer Client methods when possible; Raw is for subscription management
	// and low-level operations not covered by the interface.
	Raw() *nats.Conn
}

// natsClient is the production implementation backed by astra mq/nats.
type natsClient struct {
	producer *astranats.Producer // astra nats.Producer (satisfies mq.Producer)
	conn     *nats.Conn          // raw conn for Request (nats.Producer wraps this)
}

// New creates a new NATS client connected to the given URL.
func New(url string) (Client, error) {
	cfg := astranats.Config{URL: url}
	producer, err := astranats.NewProducer(cfg)
	if err != nil {
		return nil, fmt.Errorf("natsclient: create producer: %w", err)
	}
	return &natsClient{
		producer: producer,
		conn:     producer.Conn(),
	}, nil
}

// Compile-time assertion: natsClient implements Client.
var _ Client = (*natsClient)(nil)

// Publish sends a message to the given subject.
func (c *natsClient) Publish(subject string, data []byte) error {
	return c.producer.Publish(context.Background(), &mq.Message{
		Topic:   subject,
		Payload: data,
	})
}

// Request performs a synchronous request-response with a timeout.
func (c *natsClient) Request(subject string, data []byte, timeout time.Duration) (*nats.Msg, error) {
	return c.conn.Request(subject, data, timeout)
}

// Subscribe subscribes to a subject with a callback.
func (c *natsClient) Subscribe(subject string, cb nats.MsgHandler) (*nats.Subscription, error) {
	return c.conn.Subscribe(subject, cb)
}

// QueueSubscribe subscribes to a subject in a queue group.
func (c *natsClient) QueueSubscribe(subject, queue string, cb nats.MsgHandler) (*nats.Subscription, error) {
	return c.conn.QueueSubscribe(subject, queue, cb)
}

// Close closes the NATS connection.
func (c *natsClient) Close() {
	c.producer.Close()
}

// ─── astra Producer accessor ─────────────────────────────────────────────────

// Raw returns the underlying *nats.Conn for advanced usage.
// Use with caution — prefer Client methods when possible.
func (c *natsClient) Raw() *nats.Conn {
	return c.conn
}

// Producer returns the underlying astra mq.Producer for advanced usage.
func (c *natsClient) Producer() *astranats.Producer {
	return c.producer
}
