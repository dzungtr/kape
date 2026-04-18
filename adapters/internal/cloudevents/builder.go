package cloudevents

import (
	"fmt"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/google/uuid"
)

// AlertInput contains the fields needed to build a CloudEvent from an AlertManager alert.
type AlertInput struct {
	Subject      string
	Job          string
	Alertname    string
	Labels       map[string]string
	Annotations  map[string]string
	StartsAt     time.Time
	GeneratorURL string
}

// Build constructs a CloudEvents 1.0 event from AlertManager alert data.
// Returns an error if Subject is empty.
func Build(in AlertInput) (ce.Event, error) {
	if in.Subject == "" {
		return ce.Event{}, fmt.Errorf("missing kape_subject label on alert %q", in.Alertname)
	}

	event := ce.NewEvent()
	event.SetSpecVersion("1.0")
	event.SetType(in.Subject)
	event.SetSource(fmt.Sprintf("alertmanager/%s", in.Job))
	event.SetID(uuid.New().String())
	event.SetTime(in.StartsAt)
	event.SetDataContentType("application/json")

	data := map[string]any{
		"alertname":    in.Alertname,
		"labels":       in.Labels,
		"annotations":  in.Annotations,
		"startsAt":     in.StartsAt,
		"generatorURL": in.GeneratorURL,
	}
	if err := event.SetData("application/json", data); err != nil {
		return ce.Event{}, fmt.Errorf("setting event data: %w", err)
	}

	return event, nil
}
