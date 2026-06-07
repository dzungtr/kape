package reconcile_test

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	domainconfig "github.com/kape-io/kape/operator/domain/config"
)

// findCondition returns the condition with the given type from the slice, or
// nil. Shared across the reconcile_test files for status assertions.
func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}

// fakeConfigLoader is a no-op KapeConfigLoader returning the zero config.
type fakeConfigLoader struct{}

func (f *fakeConfigLoader) Load(_ context.Context) (domainconfig.KapeConfig, error) {
	return domainconfig.KapeConfig{}, nil
}
