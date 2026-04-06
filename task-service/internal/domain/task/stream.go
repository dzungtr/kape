package task

// Stream is the port for SSE fan-out. Implemented by infrastructure/sse.
type Stream interface {
	Publish(t *Task)
	Subscribe() (<-chan *Task, func()) // returns channel and unsubscribe func
}
