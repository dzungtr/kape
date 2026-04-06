package http_test

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kape-io/kape/task-service/internal/domain/task"
	"github.com/kape-io/kape/task-service/internal/infrastructure/sse"
	httpAdapter "github.com/kape-io/kape/task-service/internal/interfaces/http"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSSEHandler_DeliversPublishedTask(t *testing.T) {
	hub := sse.NewHub()
	handler := httpAdapter.NewSSEHandler(hub)

	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest(http.MethodGet, "/tasks/stream", nil).WithContext(ctx)
	defer cancel()

	rec := httptest.NewRecorder()
	done := make(chan struct{})

	go func() {
		handler.ServeHTTP(rec, r)
		close(done)
	}()

	// Let handler register subscriber
	time.Sleep(20 * time.Millisecond)

	hub.Publish(&task.Task{ID: "01SSE", Handler: "h", Status: task.StatusProcessing, EventRaw: task.EventRaw{}})
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	body := rec.Body.String()
	assert.True(t, strings.HasPrefix(body, "data:"), "expected SSE data line, got: %q", body)

	scanner := bufio.NewScanner(strings.NewReader(body))
	var found bool
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data:") {
			assert.Contains(t, line, "01SSE")
			found = true
		}
	}
	require.True(t, found, "no data line found in SSE body")
}
