package controller

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

// MapToolToHandlers maps a KapeTool change to the KapeHandlers that reference it.
// Used as a secondary watch: KapeTool changes re-enqueue referencing KapeHandlers.
func MapToolToHandlers(c client.Client) func(ctx context.Context, obj client.Object) []reconcile.Request {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		tool, ok := obj.(*v1alpha1.KapeTool)
		if !ok {
			return nil
		}
		var handlerList v1alpha1.KapeHandlerList
		if err := c.List(ctx, &handlerList, client.MatchingLabels{
			fmt.Sprintf("kape.io/tool-ref-%s", tool.Name): "true",
		}); err != nil {
			return nil
		}
		requests := make([]reconcile.Request, 0, len(handlerList.Items))
		for _, h := range handlerList.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: h.Name, Namespace: h.Namespace},
			})
		}
		return requests
	}
}

// MapSchemaToHandlers maps a KapeSchema change to the KapeHandlers that reference it.
// Used as a secondary watch: KapeSchema schemaHash changes re-enqueue referencing KapeHandlers.
func MapSchemaToHandlers(c client.Client) func(ctx context.Context, obj client.Object) []reconcile.Request {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		schema, ok := obj.(*v1alpha1.KapeSchema)
		if !ok {
			return nil
		}
		var handlerList v1alpha1.KapeHandlerList
		if err := c.List(ctx, &handlerList, client.MatchingLabels{
			"kape.io/schema-ref": schema.Name,
		}); err != nil {
			return nil
		}
		requests := make([]reconcile.Request, 0, len(handlerList.Items))
		for _, h := range handlerList.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: h.Name, Namespace: h.Namespace},
			})
		}
		return requests
	}
}
