package audit

import (
	"encoding/json"
	"time"
)

// EventList is the batched payload POSTed by the K8s API server.
type EventList struct {
	APIVersion string  `json:"apiVersion"` // "audit.k8s.io/v1"
	Kind       string  `json:"kind"`       // "EventList"
	Items      []Event `json:"items"`
}

// Event is a single K8s audit event.
type Event struct {
	AuditID                  string      `json:"auditID"`
	Stage                    string      `json:"stage"`
	RequestURI               string      `json:"requestURI"`
	Verb                     string      `json:"verb"`
	User                     UserInfo    `json:"user"`
	SourceIPs                []string    `json:"sourceIPs,omitempty"`
	UserAgent                string      `json:"userAgent,omitempty"`
	ObjectRef                *ObjectRef  `json:"objectRef,omitempty"`
	ResponseStatus           *StatusInfo `json:"responseStatus,omitempty"`
	RequestObject            *RawObject  `json:"requestObject,omitempty"`
	ResponseObject           *RawObject  `json:"responseObject,omitempty"`
	RequestReceivedTimestamp time.Time   `json:"requestReceivedTimestamp"`
	StageTimestamp           time.Time   `json:"stageTimestamp"`
}

// UserInfo represents the authenticated user on the request.
type UserInfo struct {
	Username string   `json:"username"`
	UID      string   `json:"uid,omitempty"`
	Groups   []string `json:"groups,omitempty"`
}

// ObjectRef is the Kubernetes object the request targeted.
type ObjectRef struct {
	Resource        string `json:"resource,omitempty"`
	Namespace       string `json:"namespace,omitempty"`
	Name            string `json:"name,omitempty"`
	APIVersion      string `json:"apiVersion,omitempty"`
	APIGroup        string `json:"apiGroup,omitempty"`
	ResourceVersion string `json:"resourceVersion,omitempty"`
	Subresource     string `json:"subresource,omitempty"`
}

// StatusInfo holds the HTTP response status.
type StatusInfo struct {
	Code   int32  `json:"code"`
	Status string `json:"status,omitempty"`
}

// RawObject is an arbitrary JSON object (request/response body).
// Stored as json.RawMessage to avoid re-encoding cost.
type RawObject struct {
	Raw json.RawMessage `json:"raw,omitempty"`
}
