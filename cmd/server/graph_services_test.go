package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/wachd/wachd/internal/graph"
)

func TestGraphServiceResponseFromNode(t *testing.T) {
	externalID := "checkout-api"
	raw, err := json.Marshal(map[string]string{
		"name":        "checkout-api",
		"description": "Handles checkout flow",
	})
	if err != nil {
		t.Fatalf("marshal properties: %v", err)
	}

	now := time.Now().UTC()
	node := &graph.Node{
		ID:         uuid.New(),
		Type:       graph.NodeTypeService,
		Status:     graph.NodeStatusPermanent,
		Label:      "Checkout API",
		ExternalID: &externalID,
		Properties: raw,
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	got := graphServiceResponseFromNode(node)

	if got.Name != "checkout-api" {
		t.Fatalf("expected normalized name checkout-api, got %q", got.Name)
	}

	if got.Label != "Checkout API" {
		t.Fatalf("expected label Checkout API, got %q", got.Label)
	}

	if got.Description != "Handles checkout flow" {
		t.Fatalf("expected description, got %q", got.Description)
	}
}

func TestNormalizeGraphServiceName(t *testing.T) {
	got := normalizeGraphServiceName("  Checkout-API  ")
	if got != "checkout-api" {
		t.Fatalf("expected checkout-api, got %q", got)
	}
}
