package nats_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"

	natspkg "github.com/kape-io/kape/adapters/internal/nats"
)

func startNATS(t *testing.T) (url string, cleanup func()) {
	t.Helper()
	ctx := context.Background()
	container, err := tcnats.Run(ctx, "nats:2.10-alpine")
	require.NoError(t, err)
	connStr, err := container.ConnectionString(ctx)
	require.NoError(t, err)
	return connStr, func() { _ = container.Terminate(ctx) }
}

func TestPublisher_PublishAndConsume(t *testing.T) {
	url, cleanup := startNATS(t)
	defer cleanup()

	nc, err := natsgo.Connect(url)
	require.NoError(t, err)
	defer nc.Drain()

	publisher, err := natspkg.NewPublisher(nc)
	require.NoError(t, err)

	event := ce.NewEvent()
	event.SetSpecVersion("1.0")
	event.SetType("kape.events.security.cilium")
	event.SetSource("alertmanager/cilium")
	event.SetID("test-id-001")
	event.SetTime(time.Now())
	event.SetDataContentType("application/json")
	require.NoError(t, event.SetData("application/json", map[string]string{"alertname": "TestAlert"}))

	ctx := context.Background()
	err = publisher.Publish(ctx, "kape.events.security.cilium", event)
	require.NoError(t, err)

	js, err := jetstream.New(nc)
	require.NoError(t, err)

	consumer, err := js.CreateOrUpdateConsumer(ctx, "KAPE_EVENTS", jetstream.ConsumerConfig{
		FilterSubject: "kape.events.security.cilium",
	})
	require.NoError(t, err)

	msg, err := consumer.Next(jetstream.FetchMaxWait(5 * time.Second))
	require.NoError(t, err)

	var received ce.Event
	require.NoError(t, json.Unmarshal(msg.Data(), &received))
	assert.Equal(t, "test-id-001", received.ID())
	assert.Equal(t, "kape.events.security.cilium", received.Type())
}

func TestPublisher_StreamAlreadyExists(t *testing.T) {
	url, cleanup := startNATS(t)
	defer cleanup()

	nc, err := natsgo.Connect(url)
	require.NoError(t, err)
	defer nc.Drain()

	_, err = natspkg.NewPublisher(nc)
	require.NoError(t, err)
	_, err = natspkg.NewPublisher(nc)
	require.NoError(t, err)
}

// TestPublisher_DuplicatePublishDeduplicatedByJetStream verifies that
// Publisher sets the Nats-Msg-Id header from event.ID(), so JetStream's
// publisher-side deduplication discards the second publish of the same
// event id within the stream's Duplicates window.
//
// The Duplicates window is set out-of-band on the stream after NewPublisher,
// because this test guards #27 (header) — the stream-side window is owned
// by infra (#26) once that lands. Until then this test sets the field
// directly so the assertion works.
func TestPublisher_DuplicatePublishDeduplicatedByJetStream(t *testing.T) {
	url, cleanup := startNATS(t)
	defer cleanup()

	nc, err := natsgo.Connect(url)
	require.NoError(t, err)
	defer nc.Drain()

	publisher, err := natspkg.NewPublisher(nc)
	require.NoError(t, err)

	ctx := context.Background()
	js, err := jetstream.New(nc)
	require.NoError(t, err)

	_, err = js.UpdateStream(ctx, jetstream.StreamConfig{
		Name:       "KAPE_EVENTS",
		Subjects:   []string{"kape.events.>"},
		MaxAge:     24 * time.Hour,
		Storage:    jetstream.FileStorage,
		Replicas:   1,
		Discard:    jetstream.DiscardOld,
		Duplicates: 60 * time.Second,
	})
	require.NoError(t, err)

	event := ce.NewEvent()
	event.SetSpecVersion("1.0")
	event.SetType("kape.events.security.cilium")
	event.SetSource("alertmanager/cilium")
	event.SetID("dup-test-001")
	event.SetTime(time.Now())
	event.SetDataContentType("application/json")
	require.NoError(t, event.SetData("application/json", map[string]string{"alertname": "DupTest"}))

	require.NoError(t, publisher.Publish(ctx, "kape.events.security.cilium", event))
	require.NoError(t, publisher.Publish(ctx, "kape.events.security.cilium", event))

	stream, err := js.Stream(ctx, "KAPE_EVENTS")
	require.NoError(t, err)
	info, err := stream.Info(ctx)
	require.NoError(t, err)
	assert.Equal(t, uint64(1), info.State.Msgs, "duplicate publish should not advance stream sequence")
}
