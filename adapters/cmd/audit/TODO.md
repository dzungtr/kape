# audit adapter — implementation TODO

This adapter is not yet implemented.

When this adapter is implemented, publish CloudEvents via the shared
`adapters/internal/nats/publisher.go` — it sets `Nats-Msg-Id` from the
CloudEvent ID so JetStream deduplicates duplicate publishes within the
stream's `Duplicates` window.

If a custom publish path is added, ensure `jetstream.WithMsgID(event.ID())`
is preserved on every publish call.

See: https://github.com/dzungtr/kape/issues/27
