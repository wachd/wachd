// Copyright 2025 NTC Dev
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package graph

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// NodeStatus controls whether a node participates in similarity searches
// and graph traversals. Nodes start as pending and are promoted to permanent
// when the incident is resolved. Pending nodes must never appear in
// FindSimilar or GetSubgraph results — that exclusion is the guarantee that
// makes two-phase write-back safe.
type NodeStatus string

const (
	// NodeStatusPending marks a node written during an active incident.
	// Excluded from all similarity searches and neighbour lookups.
	NodeStatusPending NodeStatus = "pending"
	// NodeStatusPermanent marks a node promoted on incident resolution.
	// Included in similarity searches and neighbour lookups.
	NodeStatusPermanent NodeStatus = "permanent"
)

// NodeType defines what kind of entity a graph node represents.
type NodeType string

const (
	NodeTypeIncident   NodeType = "incident"
	NodeTypeDeployment NodeType = "deployment"
	NodeTypeService    NodeType = "service"
	NodeTypeAlert      NodeType = "alert"
)

// EdgeType defines the relationship between two graph nodes.
type EdgeType string

const (
	// EdgeTypeCausedBy links an incident to the deployment or incident that caused it.
	EdgeTypeCausedBy EdgeType = "caused_by"
	// EdgeTypeAffects links an incident to the service it impacted.
	EdgeTypeAffects EdgeType = "affects"
	// EdgeTypeSimilarTo links two incidents with overlapping symptoms or context.
	EdgeTypeSimilarTo EdgeType = "similar_to"
	// EdgeTypeTriggered links an alert rule to the incident it created.
	EdgeTypeTriggered EdgeType = "triggered"
)

// Node is a vertex in the incident knowledge graph.
type Node struct {
	ID         uuid.UUID  `json:"id"`
	TeamID     uuid.UUID  `json:"team_id"`
	Type       NodeType   `json:"type"`
	Status     NodeStatus `json:"status"`
	Label      string     `json:"label"`
	ExternalID *string    `json:"external_id,omitempty"` // incidents.id, commit hash, service name, etc.
	Properties []byte     `json:"properties,omitempty"`  // JSONB — type-specific metadata
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// Edge is a directed relationship between two nodes.
type Edge struct {
	ID         uuid.UUID `json:"id"`
	TeamID     uuid.UUID `json:"team_id"`
	FromNodeID uuid.UUID `json:"from_node_id"`
	ToNodeID   uuid.UUID `json:"to_node_id"`
	Type       EdgeType  `json:"type"`
	Weight     float64   `json:"weight"`
	Properties []byte    `json:"properties,omitempty"` // JSONB — relationship metadata
	CreatedAt  time.Time `json:"created_at"`
}

// Graph is a subgraph result returned by GetSubgraph.
type Graph struct {
	Nodes []*Node `json:"nodes"`
	Edges []*Edge `json:"edges"`
}

// Store is the interface for reading and writing the incident knowledge graph.
// All methods are scoped to a single team — no cross-team access is possible.
type Store interface {
	// UpsertNode creates or updates a graph node.
	// Within a team, (type, external_id) uniquely identifies a node when external_id is set.
	// If external_id is nil, a new node is always created.
	UpsertNode(ctx context.Context, teamID uuid.UUID, n *Node) (*Node, error)

	// UpsertEdge creates or updates a directed edge between two nodes.
	// (from_node_id, to_node_id, type) is unique — calling again updates weight/properties.
	UpsertEdge(ctx context.Context, teamID uuid.UUID, e *Edge) (*Edge, error)

	// GetSubgraph returns a root node and all connected nodes and edges up to the
	// given traversal depth. Depth 1 returns only the root and its immediate neighbours.
	GetSubgraph(ctx context.Context, teamID uuid.UUID, rootNodeID uuid.UUID, depth int) (*Graph, error)

	// FindSimilar returns up to limit nodes of the same type whose label is
	// similar to the label of the given node. Used to surface past incidents
	// with matching context before the AI analysis runs.
	FindSimilar(ctx context.Context, teamID uuid.UUID, nodeID uuid.UUID, limit int) ([]*Node, error)

	// PromoteNode flips a node from pending to permanent, making it visible
	// to FindSimilar and GetSubgraph. Call this when the incident is resolved.
	// Implementations must use WHERE status = 'permanent' in every similarity
	// search and graph traversal — pending nodes must never leak into results.
	PromoteNode(ctx context.Context, teamID uuid.UUID, nodeID uuid.UUID) error

	// DeleteNode removes a node and all edges connected to it.
	DeleteNode(ctx context.Context, teamID uuid.UUID, nodeID uuid.UUID) error
}
