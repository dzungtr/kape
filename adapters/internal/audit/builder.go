package audit

import (
	"encoding/json"
	"fmt"

	ce "github.com/cloudevents/sdk-go/v2"
)

const subject = "kape.events.security.audit"

// EventData is the structured payload stored in the CloudEvent data field.
type EventData struct {
	Verb           string          `json:"verb"`
	Resource       string          `json:"resource"`
	Subresource    string          `json:"subresource,omitempty"`
	Namespace      string          `json:"namespace"`
	Name           string          `json:"name"`
	User           UserInfo        `json:"user"`
	UserAgent      string          `json:"userAgent,omitempty"`
	ResponseCode   int32           `json:"responseCode"`
	RequestObject  json.RawMessage `json:"requestObject"`
	ResponseObject json.RawMessage `json:"responseObject"`
	Stage          string          `json:"stage"`
	SourceIPs      []string        `json:"sourceIPs,omitempty"`
}

// BuildAudit constructs a CloudEvents 1.0 event from a K8s audit Event.
// clusterName is the value of the CLUSTER_NAME env var; use "unknown" if empty.
func BuildAudit(ev Event, clusterName string) (ce.Event, error) {
	var resource, namespace, name, subresource string
	if ev.ObjectRef != nil {
		resource = ev.ObjectRef.Resource
		namespace = ev.ObjectRef.Namespace
		name = ev.ObjectRef.Name
		subresource = ev.ObjectRef.Subresource
	}

	var responseCode int32
	if ev.ResponseStatus != nil {
		responseCode = ev.ResponseStatus.Code
	}

	reqObj := json.RawMessage("null")
	if ev.RequestObject != nil {
		reqObj = ev.RequestObject.Raw
	}
	respObj := json.RawMessage("null")
	if ev.ResponseObject != nil {
		respObj = ev.ResponseObject.Raw
	}

	data := EventData{
		Verb:           ev.Verb,
		Resource:       resource,
		Subresource:    subresource,
		Namespace:      namespace,
		Name:           name,
		User:           ev.User,
		UserAgent:      ev.UserAgent,
		ResponseCode:   responseCode,
		RequestObject:  reqObj,
		ResponseObject: respObj,
		Stage:          ev.Stage,
		SourceIPs:      ev.SourceIPs,
	}

	event := ce.NewEvent()
	event.SetSpecVersion("1.0")
	event.SetID(ev.AuditID)
	event.SetType(subject)
	event.SetSource(fmt.Sprintf("k8s-apiserver/%s", clusterName))
	event.SetTime(ev.RequestReceivedTimestamp)
	event.SetDataContentType("application/json")

	if err := event.SetData("application/json", data); err != nil {
		return ce.Event{}, fmt.Errorf("setting event data: %w", err)
	}

	return event, nil
}
