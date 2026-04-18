package cloudevents

import (
	"fmt"

	ce "github.com/cloudevents/sdk-go/v2"
	"github.com/google/uuid"

	"github.com/kape-io/kape/adapters/internal/alertmanager"
)

// Build constructs a CloudEvents 1.0 event from an AlertManager alert.
// Returns an error if the alert has no kape_subject label.
func Build(alert alertmanager.Alert) (ce.Event, error) {
	subject := alert.Labels["kape_subject"]
	if subject == "" {
		return ce.Event{}, fmt.Errorf("missing kape_subject label on alert %q", alert.Labels["alertname"])
	}

	event := ce.NewEvent()
	event.SetSpecVersion("1.0")
	event.SetType(subject)
	event.SetSource(fmt.Sprintf("alertmanager/%s", alert.Labels["job"]))
	event.SetID(uuid.New().String())
	event.SetTime(alert.StartsAt)
	event.SetDataContentType("application/json")

	data := map[string]any{
		"alertname":    alert.Labels["alertname"],
		"labels":       alert.Labels,
		"annotations":  alert.Annotations,
		"startsAt":     alert.StartsAt,
		"generatorURL": alert.GeneratorURL,
	}
	if err := event.SetData("application/json", data); err != nil {
		return ce.Event{}, fmt.Errorf("setting event data: %w", err)
	}

	return event, nil
}
