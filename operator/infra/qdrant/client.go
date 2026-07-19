package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 10 * time.Second}

type createCollectionRequest struct {
	Vectors vectorsConfig `json:"vectors"`
}

type vectorsConfig struct {
	Size     int    `json:"size"`
	Distance string `json:"distance"`
}

// EnsureCollection creates the named collection with the given distance metric.
// Idempotent: returns nil on HTTP 200 (created) or 409 (already exists).
func EnsureCollection(ctx context.Context, endpoint, name, distanceMetric string) error {
	url := strings.TrimRight(endpoint, "/") + "/collections/" + name
	body := createCollectionRequest{
		Vectors: vectorsConfig{
			Size:     1536,
			Distance: normaliseDistance(distanceMetric),
		},
	}
	b, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshalling create-collection request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("building create-collection request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling Qdrant PUT /collections/%s: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusConflict {
		return nil
	}
	return fmt.Errorf("Qdrant PUT /collections/%s returned HTTP %d", name, resp.StatusCode)
}

// DeleteCollection deletes the named collection.
// Idempotent: returns nil on HTTP 200 (deleted) or 404 (not found).
func DeleteCollection(ctx context.Context, endpoint, name string) error {
	url := strings.TrimRight(endpoint, "/") + "/collections/" + name
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, url, nil)
	if err != nil {
		return fmt.Errorf("building delete-collection request: %w", err)
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("calling Qdrant DELETE /collections/%s: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return fmt.Errorf("Qdrant DELETE /collections/%s returned HTTP %d", name, resp.StatusCode)
}

func normaliseDistance(metric string) string {
	switch strings.ToLower(metric) {
	case "dot":
		return "Dot"
	case "euclidean":
		return "Euclid"
	default:
		return "Cosine"
	}
}
