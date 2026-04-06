package task_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/kape-io/kape/task-service/internal/domain/task"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskStatus_IsTerminal(t *testing.T) {
	terminal := []task.TaskStatus{
		task.StatusCompleted,
		task.StatusFailed,
		task.StatusSchemaValidationFailed,
		task.StatusActionError,
		task.StatusUnprocessableEvent,
		task.StatusTimeout,
		task.StatusRetried,
	}
	nonTerminal := []task.TaskStatus{
		task.StatusProcessing,
		task.StatusPendingApproval,
	}
	for _, s := range terminal {
		assert.True(t, s.IsTerminal(), "expected %s to be terminal", s)
	}
	for _, s := range nonTerminal {
		assert.False(t, s.IsTerminal(), "expected %s to be non-terminal", s)
	}
}

func TestTaskStatus_AllNineStatusesDefined(t *testing.T) {
	all := []task.TaskStatus{
		task.StatusProcessing,
		task.StatusCompleted,
		task.StatusFailed,
		task.StatusSchemaValidationFailed,
		task.StatusActionError,
		task.StatusUnprocessableEvent,
		task.StatusPendingApproval,
		task.StatusTimeout,
		task.StatusRetried,
	}
	assert.Len(t, all, 9)
}

func TestActions_RoundTrip(t *testing.T) {
	original := task.Actions{
		{Name: "notify-slack", Type: "webhook", Status: "Completed", DryRun: false, Error: nil},
		{Name: "create-pr", Type: "event-emitter", Status: "Failed", DryRun: false, Error: ptr("timeout")},
	}
	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded task.Actions
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, original, decoded)
}

func TestTaskError_RoundTrip(t *testing.T) {
	original := &task.TaskError{
		Type:      "SchemaValidationFailed",
		Detail:    "field 'confidence' must be between 0 and 1",
		Schema:    ptr("karpenter-schema"),
		Raw:       nil,
		Traceback: nil,
	}
	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded task.TaskError
	require.NoError(t, json.Unmarshal(data, &decoded))
	assert.Equal(t, *original, decoded)
}

func TestNewTask_SetsDefaults(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	tsk := task.NewTask(task.CreateParams{
		ID:          "01JKXYZ",
		Cluster:     "prod",
		Handler:     "falco-handler",
		Namespace:   "kape-system",
		EventID:     "evt-001",
		EventSource: "alertmanager",
		EventType:   "kape.events.alertmanager",
		EventRaw:    task.EventRaw{"specversion": "1.0"},
		DryRun:      false,
		ReceivedAt:  now,
	})
	assert.Equal(t, "01JKXYZ", tsk.ID)
	assert.Equal(t, task.StatusProcessing, tsk.Status)
	assert.Nil(t, tsk.SchemaOutput)
	assert.Nil(t, tsk.Actions)
	assert.Nil(t, tsk.Error)
	assert.Nil(t, tsk.RetryOf)
}

func ptr(s string) *string { return &s }
