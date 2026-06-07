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
	js jetstream.JetStream
}

// NewPublisher connects to JetStream, looks up the KAPE_EVENTS stream that
// must already exist, and returns a ready Publisher.
// If the stream is absent, it returns an actionable error directing the
// operator to apply the nats.stream Helm values or the NACK Stream CR.
// The caller is responsible for managing context deadlines when calling Publish.
func NewPublisher(nc *natsgo.Conn) (*Publisher, error) {
	js, err := jetstream.New(nc)
	if err != nil {
		return nil, fmt.Errorf("creating jetstream context: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if _, err = js.Stream(ctx, streamName); err != nil {
		return nil, fmt.Errorf(
			"stream %s not found — ensure the Helm chart nats.stream job has run "+
				"or set nack.enabled=true to provision via NACK Stream CR: %w",
			streamName, err,
		)
	}

	return &Publisher{js: js}, nil
}

// Publish serialises the CloudEvent and publishes it to the given NATS subject.
// Retries with exponential backoff (max 30s interval) until ctx is cancelled or
// its deadline fires. The caller owns the timeout on ctx.
func (p *Publisher) Publish(ctx context.Context, subject string, event ce.Event) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshalling cloud event: %w", err)
	}

	bo := backoff.NewExponentialBackOff()
	bo.MaxInterval = 30 * time.Second
	bo.MaxElapsedTime = 0 // context deadline is the sole termination condition

	op := func() error {
		_, pubErr := p.js.Publish(ctx, subject, data, jetstream.WithMsgID(event.ID()))
		return pubErr
	}

	if err := backoff.Retry(op, backoff.WithContext(bo, ctx)); err != nil {
		return fmt.Errorf("publishing to %s: %w", subject, err)
	}
	return nil
}
