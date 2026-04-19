package reconcile

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
	"github.com/kape-io/kape/operator/infra/ports"
)

const schemaFinalizer = "kape.io/schema-protection"

// SchemaReconciler performs the full reconcile logic for KapeSchema.
type SchemaReconciler struct {
	schemas ports.SchemaRepository
}

// NewSchemaReconciler creates a SchemaReconciler.
func NewSchemaReconciler(schemas ports.SchemaRepository) *SchemaReconciler {
	return &SchemaReconciler{schemas: schemas}
}

// Reconcile implements the KapeSchema reconcile loop.
func (r *SchemaReconciler) Reconcile(ctx context.Context, key types.NamespacedName) (ctrl.Result, error) {
	schema, err := r.schemas.Get(ctx, key)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching KapeSchema: %w", err)
	}
	if schema == nil {
		return ctrl.Result{}, nil
	}

	// 1. Validate spec.jsonSchema
	if err := validateJSONSchema(schema); err != nil {
		schema.Status.Conditions = setCondition(schema.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "InvalidSchema",
			Message: err.Error(),
		})
		_ = r.schemas.UpdateStatus(ctx, schema)
		return ctrl.Result{}, nil // terminal
	}

	// 2. Manage finalizer
	if err := r.schemas.AddFinalizer(ctx, schema, schemaFinalizer); err != nil {
		return ctrl.Result{}, fmt.Errorf("adding finalizer: %w", err)
	}

	// 3. Handle deletion
	if !schema.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, schema)
	}

	// 4. Compute and write schemaHash
	hash, err := computeSchemaHash(schema)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("computing schema hash: %w", err)
	}
	schema.Status.SchemaHash = hash

	// 5. Set Ready=True
	schema.Status.Conditions = setCondition(schema.Status.Conditions, metav1.Condition{
		Type:    "Ready",
		Status:  metav1.ConditionTrue,
		Reason:  "Valid",
		Message: "JSON Schema validated successfully",
	})
	if err := r.schemas.UpdateStatus(ctx, schema); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *SchemaReconciler) handleDeletion(ctx context.Context, schema *v1alpha1.KapeSchema) (ctrl.Result, error) {
	handlers, err := r.schemas.ListHandlersBySchemaRef(ctx, schema.Name)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("listing handlers: %w", err)
	}
	if len(handlers) > 0 {
		names := make([]string, 0, len(handlers))
		for _, h := range handlers {
			names = append(names, h.Name)
		}
		schema.Status.Conditions = setCondition(schema.Status.Conditions, metav1.Condition{
			Type:    "Ready",
			Status:  metav1.ConditionFalse,
			Reason:  "ReferencedByHandlers",
			Message: fmt.Sprintf("Cannot delete: referenced by handlers: [%s]", strings.Join(names, ", ")),
		})
		_ = r.schemas.UpdateStatus(ctx, schema)
		return ctrl.Result{}, nil // blocked — no requeue; re-triggered on handler deletion
	}
	if err := r.schemas.RemoveFinalizer(ctx, schema, schemaFinalizer); err != nil {
		return ctrl.Result{}, fmt.Errorf("removing finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

func validateJSONSchema(schema *v1alpha1.KapeSchema) error {
	js := schema.Spec.JSONSchema
	if js.Type != "object" {
		return fmt.Errorf("spec.jsonSchema.type must be 'object', got %q", js.Type)
	}
	for _, req := range js.Required {
		if _, ok := js.Properties[req]; !ok {
			return fmt.Errorf("required field %q not found in properties", req)
		}
	}
	return nil
}

func computeSchemaHash(schema *v1alpha1.KapeSchema) (string, error) {
	b, err := json.Marshal(schema.Spec)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(b)), nil
}
