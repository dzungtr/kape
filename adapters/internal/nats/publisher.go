package nats

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cenkalti/backoff/v4"
	ce "github.com/cloudevents/sdk-go/v2"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const streamName = "KAPE_EVENTS"

// Publisher publishes CloudEvents to NATS JetStream with retry/backoff.
type Publisher struct {
	js         jetstream.JetStream
	publishTTL time.Duration
}

// NewPublisher connects to JetStream, provisions the KAPE_EVENTS stream if it
// does not exist, and returns a ready Publisher.
func NewPublisher(nc *natsgo.Conn, publishTTL time.Duration) (*Publisher, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("creating jetstream context: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     streamName,
		Subjects: []string{"kape.events.>"},
		MaxAge:   24 * time.Hour,
		Storage:  jetstream.FileStorage,
		Replicas: 1, // Helm overrides to 3 in production
		Discard:  jetstream.DiscardOld,
	})
	if err != nil {
		return nil, fmt.Errorf("provisioning stream %s: %w", streamName, err)
	}

	return &Publisher{js: js, publishTTL: publishTTL}, nil
}

// Publish serialises the CloudEvent and publishes it to the given NATS subject.
// Retries with exponential backoff (max 30s interval) until publishTTL expires.
func (p *Publisher) Publish(ctx context.Context, subject string, event ce.Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshalling cloud event: %w", err)
	}

	bo := backoff.NewExponentialBackOff()
	bo.MaxInterval = 30 * time.Second

	deadline := time.Now().Add(p.publishTTL)
	ttlCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	op := func() error {
		_, pubErr := p.js.Publish(ttlCtx, subject, data)
		return pubErr
	}

	if err := backoff.Retry(op, backoff.WithContext(bo, ttlCtx)); err != nil {
		return fmt.Errorf("publishing to %s: %w", subject, err)
	}
	return nil
}
