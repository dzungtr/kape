package controller_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/kape-io/kape/operator/controller"
	v1alpha1 "github.com/kape-io/kape/operator/infra/api/v1alpha1"
)

func newWatchScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = v1alpha1.AddToScheme(s)
	return s
}

func handlerWithLabel(name, ns, labelKey, labelVal string) *v1alpha1.KapeHandler {
	return &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
			Labels:    map[string]string{labelKey: labelVal},
		},
	}
}

func TestMapSkillToHandlers_EnqueuesLabelledHandlers(t *testing.T) {
	s := newWatchScheme()

	skill := &v1alpha1.KapeSkill{
		ObjectMeta: metav1.ObjectMeta{Name: "analyst", Namespace: "kape-system"},
	}
	h1 := handlerWithLabel("handler-a", "kape-system", "kape.io/skill-ref-analyst", "true")
	h2 := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "handler-b", Namespace: "kape-system"},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(skill, h1, h2).Build()

	mapper := controller.MapSkillToHandlers(c)
	requests := mapper(context.Background(), skill)

	require.Len(t, requests, 1)
	assert.Equal(t, types.NamespacedName{Name: "handler-a", Namespace: "kape-system"}, requests[0].NamespacedName)
}

func TestMapSkillToHandlers_NoHandlers_ReturnsEmpty(t *testing.T) {
	s := newWatchScheme()
	skill := &v1alpha1.KapeSkill{
		ObjectMeta: metav1.ObjectMeta{Name: "analyst", Namespace: "kape-system"},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(skill).Build()

	mapper := controller.MapSkillToHandlers(c)
	requests := mapper(context.Background(), skill)

	assert.Empty(t, requests)
}

func TestMapSkillToHandlers_WrongObjectType_ReturnsNil(t *testing.T) {
	s := newWatchScheme()
	notASkill := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "handler-a", Namespace: "kape-system"},
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(notASkill).Build()

	mapper := controller.MapSkillToHandlers(c)
	requests := mapper(context.Background(), notASkill)

	assert.Nil(t, requests)
}

func TestMapSkillToHandlers_MultipleHandlers_EnqueuesAll(t *testing.T) {
	s := newWatchScheme()
	skill := &v1alpha1.KapeSkill{
		ObjectMeta: metav1.ObjectMeta{Name: "analyst", Namespace: "kape-system"},
	}
	h1 := handlerWithLabel("handler-a", "kape-system", "kape.io/skill-ref-analyst", "true")
	h2 := handlerWithLabel("handler-b", "kape-system", "kape.io/skill-ref-analyst", "true")
	h3 := &v1alpha1.KapeHandler{
		ObjectMeta: metav1.ObjectMeta{Name: "handler-c", Namespace: "kape-system"},
	}

	c := fake.NewClientBuilder().WithScheme(s).WithObjects(skill, h1, h2, h3).Build()

	mapper := controller.MapSkillToHandlers(c)
	requests := mapper(context.Background(), skill)

	require.Len(t, requests, 2)
	names := []string{requests[0].NamespacedName.Name, requests[1].NamespacedName.Name}
	assert.Contains(t, names, "handler-a")
	assert.Contains(t, names, "handler-b")
}
