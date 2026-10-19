package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/kape-io/kape/operator/domain/agentserver"
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

// MapSkillToHandlers maps a KapeSkill change to the KapeHandlers that reference it.
// Used as a secondary watch: KapeSkill changes re-enqueue referencing KapeHandlers.
// Depends on Slice 3 having written kape.io/skill-ref-{name}=true labels on KapeHandler resources.
func MapSkillToHandlers(c client.Client) func(ctx context.Context, obj client.Object) []reconcile.Request {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		skill, ok := obj.(*v1alpha1.KapeSkill)
		if !ok {
			return nil
		}
		var handlerList v1alpha1.KapeHandlerList
		if err := c.List(ctx, &handlerList, client.MatchingLabels{
			fmt.Sprintf("kape.io/skill-ref-%s", skill.Name): "true",
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

// MapSecretToAgentServers maps a credential Secret change to the
// KapeAgentServers in its namespace that reference it, so editing a Secret
// re-pushes the provider record (rotation = edit the Secret, ADR-0088).
//
// Only Secrets labelled kape.io/credential=true are considered: everything else
// is outside the operator's credential read path (D10) and stays untracked.
func MapSecretToAgentServers(c client.Client) func(ctx context.Context, obj client.Object) []reconcile.Request {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		secret, ok := obj.(*corev1.Secret)
		if !ok {
			return nil
		}
		if secret.Labels[agentserver.CredentialLabel] != agentserver.CredentialLabelValue {
			return nil
		}
		var serverList v1alpha1.KapeAgentServerList
		if err := c.List(ctx, &serverList, client.InNamespace(secret.Namespace)); err != nil {
			return nil
		}
		var requests []reconcile.Request
		for _, server := range serverList.Items {
			if !referencesSecret(server, secret.Name) {
				continue
			}
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: server.Name, Namespace: server.Namespace},
			})
		}
		return requests
	}
}

// referencesSecret reports whether any credentials[] entry of the server names
// the given Secret.
func referencesSecret(server v1alpha1.KapeAgentServer, secretName string) bool {
	for _, cred := range server.Spec.Credentials {
		if cred.SecretRef.Name == secretName {
			return true
		}
	}
	return false
}

// CredentialSecretPredicate limits the operator's Secret watch to
// credential-labelled Secrets; an unlabelled Secret never enters the cache.
func CredentialSecretPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		return obj.GetLabels()[agentserver.CredentialLabel] == agentserver.CredentialLabelValue
	})
}
