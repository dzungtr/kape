package audit_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kape-io/kape/adapters/internal/audit"
)

func baseEvent(auditID, verb string) audit.Event {
	return audit.Event{
		AuditID: auditID,
		Stage:   "ResponseComplete",
		Verb:    verb,
		User:    audit.UserInfo{Username: "admin"},
		ObjectRef: &audit.ObjectRef{
			Resource:  "secrets",
			Namespace: "prod",
			Name:      "db-creds",
		},
		ResponseStatus:           &audit.StatusInfo{Code: 200},
		RequestReceivedTimestamp: time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC),
		StageTimestamp:           time.Date(2026, 5, 17, 12, 0, 1, 0, time.UTC),
	}
}

func TestBuildAudit_FullEvent(t *testing.T) {
	ev := baseEvent("audit-uuid-1234", "create")
	ev.RequestObject = &audit.RawObject{Raw: json.RawMessage(`{"key":"value"}`)}
	ev.ResponseObject = &audit.RawObject{Raw: json.RawMessage(`{"status":"ok"}`)}

	ce, err := audit.BuildAudit(ev, "prod-cluster")
	require.NoError(t, err)

	assert.Equal(t, "audit-uuid-1234", ce.ID())
	assert.Equal(t, "kape.events.security.audit", ce.Type())
	assert.Equal(t, "k8s-apiserver/prod-cluster", ce.Source())
	assert.Equal(t, time.Date(2026, 5, 17, 12, 0, 0, 0, time.UTC), ce.Time())
	assert.Equal(t, "application/json", ce.DataContentType())

	var data map[string]any
	require.NoError(t, json.Unmarshal(ce.Data(), &data))
	assert.Equal(t, "create", data["verb"])
	assert.Equal(t, "secrets", data["resource"])
	assert.Equal(t, "prod", data["namespace"])
	assert.Equal(t, "db-creds", data["name"])
	assert.Equal(t, float64(200), data["responseCode"])
	assert.Equal(t, "ResponseComplete", data["stage"])
}

func TestBuildAudit_NilObjectRef(t *testing.T) {
	ev := baseEvent("audit-nil-ref", "list")
	ev.ObjectRef = nil

	ce, err := audit.BuildAudit(ev, "dev-cluster")
	require.NoError(t, err)

	var data map[string]any
	require.NoError(t, json.Unmarshal(ce.Data(), &data))
	assert.Equal(t, "", data["resource"])
	assert.Equal(t, "", data["namespace"])
	assert.Equal(t, "", data["name"])
}

func TestBuildAudit_NilResponseStatus(t *testing.T) {
	ev := baseEvent("audit-nil-status", "delete")
	ev.ResponseStatus = nil

	ce, err := audit.BuildAudit(ev, "dev-cluster")
	require.NoError(t, err)

	var data map[string]any
	require.NoError(t, json.Unmarshal(ce.Data(), &data))
	assert.Equal(t, float64(0), data["responseCode"])
}

func TestBuildAudit_UsesAuditID(t *testing.T) {
	ev := baseEvent("my-specific-audit-id", "get")

	ce, err := audit.BuildAudit(ev, "any-cluster")
	require.NoError(t, err)

	assert.Equal(t, "my-specific-audit-id", ce.ID())
}

func TestBuildAudit_NilRequestResponseObjects(t *testing.T) {
	ev := baseEvent("audit-nil-objects", "get")
	ev.RequestObject = nil
	ev.ResponseObject = nil

	ce, err := audit.BuildAudit(ev, "dev-cluster")
	require.NoError(t, err)

	var data map[string]any
	require.NoError(t, json.Unmarshal(ce.Data(), &data))
	assert.Nil(t, data["requestObject"])
	assert.Nil(t, data["responseObject"])
}
