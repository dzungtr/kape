package qdrant_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kape-io/kape/operator/infra/qdrant"
)

func TestEnsureCollection_Idempotent200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodPut, r.Method)
		assert.Equal(t, "/collections/my-tool", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := qdrant.EnsureCollection(context.Background(), srv.URL, "my-tool", "cosine")
	require.NoError(t, err)
}

func TestEnsureCollection_Idempotent409(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()

	err := qdrant.EnsureCollection(context.Background(), srv.URL, "my-tool", "dot")
	require.NoError(t, err)
}

func TestEnsureCollection_ErrorOnOtherStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := qdrant.EnsureCollection(context.Background(), srv.URL, "my-tool", "cosine")
	require.Error(t, err)
}

func TestDeleteCollection_Idempotent200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodDelete, r.Method)
		assert.Equal(t, "/collections/my-tool", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := qdrant.DeleteCollection(context.Background(), srv.URL, "my-tool")
	require.NoError(t, err)
}

func TestDeleteCollection_Idempotent404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	err := qdrant.DeleteCollection(context.Background(), srv.URL, "my-tool")
	require.NoError(t, err)
}

func TestDeleteCollection_ErrorOnOtherStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	err := qdrant.DeleteCollection(context.Background(), srv.URL, "my-tool")
	require.Error(t, err)
}
