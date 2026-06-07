package gen

import "time"

// HandlerAggregate is a per-handler summary derived from the tasks table.
type HandlerAggregate struct {
	Handler         string     `json:"handler"`
	Namespace       string     `json:"namespace"`
	LastTaskAt      *time.Time `json:"last_task_at"`
	Tasks24H        int32      `json:"tasks_24h"`
	Failures24H     int32      `json:"failures_24h"`
	ProcessingCount int32      `json:"processing_count"`
}

// AssertHandlerAggregateRequired checks if the required fields are not zero-ed
func AssertHandlerAggregateRequired(obj HandlerAggregate) error {
	elements := map[string]interface{}{
		"handler":   obj.Handler,
		"namespace": obj.Namespace,
	}
	for name, el := range elements {
		if isZero := IsZeroValue(el); isZero {
			return &RequiredError{Field: name}
		}
	}
	return nil
}

// AssertHandlerAggregateConstraints checks if the values respects the defined constraints
func AssertHandlerAggregateConstraints(obj HandlerAggregate) error {
	return nil
}
