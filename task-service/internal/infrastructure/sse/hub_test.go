package sse_test

import (
	"testing"
	"time"

	"github.com/kape-io/kape/task-service/internal/domain/task"
	"github.com/kape-io/kape/task-service/internal/infrastructure/sse"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeTask(id string) *task.Task {
	return &task.Task{ID: id, Handler: "test-handler", Status: task.StatusProcessing}
}

func TestHub_PublishDeliveredToSubscriber(t *testing.T) {
	hub := sse.NewHub()
	ch, unsub := hub.Subscribe()
	defer unsub()

	hub.Publish(makeTask("01A"))

	select {
	case received := <-ch:
		assert.Equal(t, "01A", received.ID)
	case <-time.After(time.Second):
		t.Fatal("expected task on channel, got timeout")
	}
}

func TestHub_MultipleSubscribersEachReceive(t *testing.T) {
	hub := sse.NewHub()
	ch1, unsub1 := hub.Subscribe()
	ch2, unsub2 := hub.Subscribe()
	defer unsub1()
	defer unsub2()

	hub.Publish(makeTask("01B"))

	assertReceived := func(ch <-chan *task.Task, label string) {
		select {
		case got := <-ch:
			assert.Equal(t, "01B", got.ID, label)
		case <-time.After(time.Second):
			t.Fatalf("%s: timeout waiting for task", label)
		}
	}
	assertReceived(ch1, "subscriber1")
	assertReceived(ch2, "subscriber2")
}

func TestHub_SlowSubscriberDoesNotBlockOthers(t *testing.T) {
	hub := sse.NewHub()

	// fast subscriber
	chFast, unsubFast := hub.Subscribe()
	defer unsubFast()

	// slow subscriber: deliberately don't read from it
	_, unsubSlow := hub.Subscribe()
	defer unsubSlow()

	// Publish enough to fill the slow subscriber's buffer (16) plus one
	for i := 0; i < 20; i++ {
		hub.Publish(makeTask("fill"))
	}

	// fast subscriber should still receive (at least the first 16 or up to buffer)
	received := 0
	for {
		select {
		case <-chFast:
			received++
		default:
			goto done
		}
	}
done:
	assert.Greater(t, received, 0, "fast subscriber should receive tasks")
}

func TestHub_UnsubscribeStopsDelivery(t *testing.T) {
	hub := sse.NewHub()
	ch, unsub := hub.Subscribe()
	unsub() // unsubscribe immediately

	hub.Publish(makeTask("01C"))

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("expected channel to be closed or empty after unsubscribe")
		}
	default:
		// channel is empty and closed — correct
	}
}

// Ensure require is used at least once to avoid import error
var _ = require.NoError
