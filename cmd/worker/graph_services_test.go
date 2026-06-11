package main

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/wachd/wachd/internal/graph"
)

type serviceEdgeGraphStore struct {
	serviceNode *graph.Node
	findErr     error
	edge        *graph.Edge
	externalID  string
}

func (s *serviceEdgeGraphStore) UpsertNode(ctx context.Context, teamID uuid.UUID, n *graph.Node) (*graph.Node, error) {
	panic("not used")
}

func (s *serviceEdgeGraphStore) UpsertEdge(ctx context.Context, teamID uuid.UUID, e *graph.Edge) (*graph.Edge, error) {
	s.edge = e
	return e, nil
}

func (s *serviceEdgeGraphStore) GetSubgraph(ctx context.Context, teamID uuid.UUID, rootNodeID uuid.UUID, depth int) (*graph.Graph, error) {
	panic("not used")
}

func (s *serviceEdgeGraphStore) FindSimilar(ctx context.Context, teamID uuid.UUID, nodeID uuid.UUID, limit int) ([]*graph.SimilarNode, error) {
	panic("not used")
}

func (s *serviceEdgeGraphStore) FindNodeByExternalID(ctx context.Context, teamID uuid.UUID, nodeType graph.NodeType, externalID string) (*graph.Node, error) {
	s.externalID = externalID

	if s.findErr != nil {
		return nil, s.findErr
	}

	if s.serviceNode == nil {
		return nil, graph.ErrNodeNotFound
	}

	return s.serviceNode, nil
}

func (s *serviceEdgeGraphStore) ListNodes(ctx context.Context, teamID uuid.UUID, status graph.NodeStatus, limit int) ([]*graph.Node, error) {
	panic("not used")
}

func (s *serviceEdgeGraphStore) PromoteNode(ctx context.Context, teamID uuid.UUID, nodeID uuid.UUID) error {
	panic("not used")
}

func (s *serviceEdgeGraphStore) DeleteNode(ctx context.Context, teamID uuid.UUID, nodeID uuid.UUID) error {
	panic("not used")
}

func TestWriteAffectsServiceEdgeCreatesEdgeForDeclaredService(t *testing.T) {
	teamID := uuid.New()
	incidentNodeID := uuid.New()
	serviceNodeID := uuid.New()

	store := &serviceEdgeGraphStore{
		serviceNode: &graph.Node{
			ID:     serviceNodeID,
			TeamID: teamID,
			Type:   graph.NodeTypeService,
			Status: graph.NodeStatusPermanent,
			Label:  "Checkout API",
		},
	}

	writeAffectsServiceEdge(context.Background(), store, teamID, incidentNodeID, " Checkout-API ")

	if store.externalID != "checkout-api" {
		t.Fatalf("expected normalized lookup checkout-api, got %q", store.externalID)
	}

	if store.edge == nil {
		t.Fatal("expected affects edge to be written")
	}

	if store.edge.FromNodeID != incidentNodeID {
		t.Fatalf("expected from node %s, got %s", incidentNodeID, store.edge.FromNodeID)
	}

	if store.edge.ToNodeID != serviceNodeID {
		t.Fatalf("expected to node %s, got %s", serviceNodeID, store.edge.ToNodeID)
	}

	if store.edge.Type != graph.EdgeTypeAffects {
		t.Fatalf("expected affects edge, got %s", store.edge.Type)
	}

	if store.edge.Status != graph.EdgeStatusPermanent {
		t.Fatalf("expected permanent edge, got %s", store.edge.Status)
	}
}

func TestWriteAffectsServiceEdgeSkipsMissingService(t *testing.T) {
	store := &serviceEdgeGraphStore{}

	writeAffectsServiceEdge(context.Background(), store, uuid.New(), uuid.New(), "checkout-api")

	if store.edge != nil {
		t.Fatalf("expected no edge for missing service, got %+v", store.edge)
	}
}

func TestWriteAffectsServiceEdgeSkipsEmptyServiceName(t *testing.T) {
	store := &serviceEdgeGraphStore{}

	writeAffectsServiceEdge(context.Background(), store, uuid.New(), uuid.New(), "   ")

	if store.externalID != "" {
		t.Fatalf("expected no lookup for empty service name, got lookup %q", store.externalID)
	}

	if store.edge != nil {
		t.Fatalf("expected no edge for empty service name, got %+v", store.edge)
	}
}
