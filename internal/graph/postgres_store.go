// Copyright 2025 NTC Dev
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package graph

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultSimilarityCandidateLimit = 200
	defaultMinimumSimilarityScore   = 0.12
)

// ErrNodeNotFound is returned when a graph operation cannot find the requested
// node inside the requested team boundary.
var ErrNodeNotFound = errors.New("graph node not found")

// PostgresStore is the PostgreSQL implementation of Store.
//
// It stores graph nodes and edges in the adjacency tables created by
// internal/store/schema.sql. All operations are scoped by team_id.
type PostgresStore struct {
	pool               *pgxpool.Pool
	candidateLimit     int
	minSimilarityScore float64
}

var _ Store = (*PostgresStore)(nil)

// NewPostgresStore creates a Store backed by PostgreSQL.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{
		pool:               pool,
		candidateLimit:     defaultSimilarityCandidateLimit,
		minSimilarityScore: defaultMinimumSimilarityScore,
	}
}

// UpsertNode creates or updates a graph node.
//
// If ExternalID is set, the tuple (team_id, type, external_id) is treated as the
// stable identity for the node. Re-upserting a permanent node never demotes it
// back to pending.
func (s *PostgresStore) UpsertNode(ctx context.Context, teamID uuid.UUID, n *Node) (*Node, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if n == nil {
		return nil, errors.New("graph node is required")
	}
	if teamID == uuid.Nil {
		return nil, errors.New("team id is required")
	}
	if n.TeamID != uuid.Nil && n.TeamID != teamID {
		return nil, fmt.Errorf("node team %s does not match requested team %s", n.TeamID, teamID)
	}
	if n.ID == uuid.Nil {
		n.ID = uuid.New()
	}
	if n.Status == "" {
		n.Status = NodeStatusPending
	}
	if strings.TrimSpace(n.Label) == "" {
		return nil, errors.New("graph node label is required")
	}
	if n.ExternalID != nil && strings.TrimSpace(*n.ExternalID) == "" {
		n.ExternalID = nil
	}

	row := s.pool.QueryRow(ctx, `
		INSERT INTO graph_nodes (
			id,
			team_id,
			type,
			status,
			label,
			external_id,
			properties
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (team_id, type, external_id)
		WHERE external_id IS NOT NULL
		DO UPDATE SET
			status = CASE
				WHEN graph_nodes.status = 'permanent' THEN graph_nodes.status
				ELSE EXCLUDED.status
			END,
			label = EXCLUDED.label,
			properties = EXCLUDED.properties,
			updated_at = now()
		RETURNING id, team_id, type, status, label, external_id, properties, created_at, updated_at
	`, n.ID, teamID, n.Type, n.Status, n.Label, n.ExternalID, n.Properties)

	created, err := scanNode(row)
	if err != nil {
		return nil, fmt.Errorf("upsert graph node: %w", err)
	}

	return created, nil
}

// UpsertEdge creates or updates a directed relationship between two nodes.
//
// Re-upserting a permanent edge never demotes it back to pending.
func (s *PostgresStore) UpsertEdge(ctx context.Context, teamID uuid.UUID, e *Edge) (*Edge, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if e == nil {
		return nil, errors.New("graph edge is required")
	}
	if teamID == uuid.Nil {
		return nil, errors.New("team id is required")
	}
	if e.TeamID != uuid.Nil && e.TeamID != teamID {
		return nil, fmt.Errorf("edge team %s does not match requested team %s", e.TeamID, teamID)
	}
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	if e.FromNodeID == uuid.Nil {
		return nil, errors.New("edge from node id is required")
	}
	if e.ToNodeID == uuid.Nil {
		return nil, errors.New("edge to node id is required")
	}
	if e.FromNodeID == e.ToNodeID {
		return nil, errors.New("edge cannot point to the same node")
	}
	if e.Status == "" {
		e.Status = EdgeStatusPending
	}
	if e.Weight <= 0 {
		e.Weight = 1
	}

	row := s.pool.QueryRow(ctx, `
		INSERT INTO graph_edges (
			id,
			team_id,
			from_id,
			to_id,
			type,
			status,
			weight,
			properties
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (team_id, from_id, to_id, type)
		DO UPDATE SET
			status = CASE
				WHEN graph_edges.status = 'permanent' THEN graph_edges.status
				ELSE EXCLUDED.status
			END,
			weight = EXCLUDED.weight,
			properties = EXCLUDED.properties,
			updated_at = now()
		RETURNING id, team_id, from_id, to_id, type, status, weight, properties, created_at, updated_at
	`, e.ID, teamID, e.FromNodeID, e.ToNodeID, e.Type, e.Status, e.Weight, e.Properties)

	created, err := scanEdge(row)
	if err != nil {
		return nil, fmt.Errorf("upsert graph edge: %w", err)
	}

	return created, nil
}

// GetSubgraph returns the requested root node plus connected permanent
// neighbours up to the requested depth.
//
// The root node may be pending so the active incident graph can be displayed.
// Neighbours and edges must be permanent so draft AI data from other active
// incidents does not leak into traversal results.
func (s *PostgresStore) GetSubgraph(ctx context.Context, teamID uuid.UUID, rootNodeID uuid.UUID, depth int) (*Graph, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if teamID == uuid.Nil {
		return nil, errors.New("team id is required")
	}
	if rootNodeID == uuid.Nil {
		return nil, errors.New("root node id is required")
	}
	if depth < 0 {
		depth = 0
	}

	root, err := s.getNode(ctx, teamID, rootNodeID)
	if err != nil {
		return nil, err
	}

	nodesByID := map[uuid.UUID]*Node{root.ID: root}
	edgesByID := map[uuid.UUID]*Edge{}

	if depth > 0 {
		rows, err := s.pool.Query(ctx, `
			WITH RECURSIVE walk(node_id, depth, path, edge_id) AS (
				SELECT $2::uuid, 0, ARRAY[$2::uuid], NULL::uuid

				UNION ALL

				SELECT
					neighbor.id,
					walk.depth + 1,
					walk.path || neighbor.id,
					e.id
				FROM walk
				JOIN graph_edges e
				  ON e.team_id = $1
				 AND e.status = 'permanent'
				 AND (e.from_id = walk.node_id OR e.to_id = walk.node_id)
				JOIN graph_nodes neighbor
				  ON neighbor.team_id = e.team_id
				 AND neighbor.id = CASE
						WHEN e.from_id = walk.node_id THEN e.to_id
						ELSE e.from_id
					 END
				 AND neighbor.status = 'permanent'
				WHERE walk.depth < $3
				  AND neighbor.id <> ALL(walk.path)
			),
			traversed_edges AS (
				SELECT DISTINCT edge_id
				FROM walk
				WHERE edge_id IS NOT NULL
			)
			SELECT
				e.id,
				e.team_id,
				e.from_id,
				e.to_id,
				e.type,
				e.status,
				e.weight,
				e.properties,
				e.created_at,
				e.updated_at,
				n.id,
				n.team_id,
				n.type,
				n.status,
				n.label,
				n.external_id,
				n.properties,
				n.created_at,
				n.updated_at
			FROM traversed_edges te
			JOIN graph_edges e
			  ON e.id = te.edge_id
			 AND e.team_id = $1
			JOIN graph_nodes n
			  ON n.team_id = e.team_id
			 AND (n.id = e.from_id OR n.id = e.to_id)
			WHERE n.id = $2
			   OR n.status = 'permanent'
			ORDER BY e.created_at, n.created_at
		`, teamID, rootNodeID, depth)
		if err != nil {
			return nil, fmt.Errorf("get subgraph: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			edge, node, err := scanEdgeAndNode(rows)
			if err != nil {
				return nil, fmt.Errorf("scan subgraph row: %w", err)
			}

			if _, exists := edgesByID[edge.ID]; !exists {
				edgesByID[edge.ID] = edge
			}

			if _, exists := nodesByID[node.ID]; !exists {
				nodesByID[node.ID] = node
			}
		}

		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("read subgraph rows: %w", err)
		}
	}

	nodes := make([]*Node, 0, len(nodesByID))
	for _, node := range nodesByID {
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].CreatedAt.Before(nodes[j].CreatedAt)
	})

	edges := make([]*Edge, 0, len(edgesByID))
	for _, edge := range edgesByID {
		edges = append(edges, edge)
	}
	sort.Slice(edges, func(i, j int) bool {
		return edges[i].CreatedAt.Before(edges[j].CreatedAt)
	})

	return &Graph{
		Nodes: nodes,
		Edges: edges,
	}, nil
}

// FindSimilar compares the requested node against permanent nodes of the same
// type in the same team.
//
// It returns the best matches ordered by descending score. Pending candidates
// are always excluded to prevent in-flight incident analysis from polluting team
// memory.
func (s *PostgresStore) FindSimilar(ctx context.Context, teamID uuid.UUID, nodeID uuid.UUID, limit int) ([]*SimilarNode, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	if teamID == uuid.Nil {
		return nil, errors.New("team id is required")
	}
	if nodeID == uuid.Nil {
		return nil, errors.New("node id is required")
	}
	if limit <= 0 {
		return []*SimilarNode{}, nil
	}

	source, err := s.getNode(ctx, teamID, nodeID)
	if err != nil {
		return nil, err
	}

	candidateLimit := s.candidateLimit
	if expandedLimit := limit * 10; expandedLimit > candidateLimit {
		candidateLimit = expandedLimit
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, team_id, type, status, label, external_id, properties, created_at, updated_at
		FROM graph_nodes
		WHERE team_id = $1
		  AND type = $2
		  AND status = 'permanent'
		  AND id <> $3
		ORDER BY updated_at DESC
		LIMIT $4
	`, teamID, source.Type, source.ID, candidateLimit)
	if err != nil {
		return nil, fmt.Errorf("query similar candidates: %w", err)
	}
	defer rows.Close()

	matches := make([]*SimilarNode, 0, limit)
	for rows.Next() {
		candidate, err := scanNode(rows)
		if err != nil {
			return nil, fmt.Errorf("scan similar candidate: %w", err)
		}

		match := scoreNodeSimilarity(source, candidate)
		if match.Score < s.minSimilarityScore {
			continue
		}

		matches = append(matches, match)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read similar candidates: %w", err)
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score == matches[j].Score {
			return matches[i].Node.CreatedAt.After(matches[j].Node.CreatedAt)
		}

		return matches[i].Score > matches[j].Score
	})

	if len(matches) > limit {
		matches = matches[:limit]
	}

	return matches, nil
}

// PromoteNode marks a node as permanent.
//
// Any connected edges are promoted only when both endpoint nodes are permanent.
func (s *PostgresStore) PromoteNode(ctx context.Context, teamID uuid.UUID, nodeID uuid.UUID) error {
	if err := s.validate(); err != nil {
		return err
	}
	if teamID == uuid.Nil {
		return errors.New("team id is required")
	}
	if nodeID == uuid.Nil {
		return errors.New("node id is required")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("promote node begin transaction: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx, `
		UPDATE graph_nodes
		SET status = 'permanent',
			updated_at = now()
		WHERE team_id = $1
		  AND id = $2
	`, teamID, nodeID)
	if err != nil {
		return fmt.Errorf("promote graph node: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNodeNotFound
	}

	if _, err := tx.Exec(ctx, `
		UPDATE graph_edges e
		SET status = 'permanent',
			updated_at = now()
		WHERE e.team_id = $1
		  AND (e.from_id = $2 OR e.to_id = $2)
		  AND EXISTS (
				SELECT 1
				FROM graph_nodes from_node
				WHERE from_node.team_id = e.team_id
				  AND from_node.id = e.from_id
				  AND from_node.status = 'permanent'
		  )
		  AND EXISTS (
				SELECT 1
				FROM graph_nodes to_node
				WHERE to_node.team_id = e.team_id
				  AND to_node.id = e.to_id
				  AND to_node.status = 'permanent'
		  )
	`, teamID, nodeID); err != nil {
		return fmt.Errorf("promote graph edges: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("promote node commit: %w", err)
	}

	return nil
}

// DeleteNode removes a node and all connected edges.
func (s *PostgresStore) DeleteNode(ctx context.Context, teamID uuid.UUID, nodeID uuid.UUID) error {
	if err := s.validate(); err != nil {
		return err
	}
	if teamID == uuid.Nil {
		return errors.New("team id is required")
	}
	if nodeID == uuid.Nil {
		return errors.New("node id is required")
	}

	tag, err := s.pool.Exec(ctx, `
		DELETE FROM graph_nodes
		WHERE team_id = $1
		  AND id = $2
	`, teamID, nodeID)
	if err != nil {
		return fmt.Errorf("delete graph node: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNodeNotFound
	}

	return nil
}

func (s *PostgresStore) validate() error {
	if s == nil {
		return errors.New("graph store is nil")
	}
	if s.pool == nil {
		return errors.New("graph store database pool is nil")
	}
	return nil
}

func (s *PostgresStore) getNode(ctx context.Context, teamID uuid.UUID, nodeID uuid.UUID) (*Node, error) {
	node, err := scanNode(s.pool.QueryRow(ctx, `
		SELECT id, team_id, type, status, label, external_id, properties, created_at, updated_at
		FROM graph_nodes
		WHERE team_id = $1
		  AND id = $2
	`, teamID, nodeID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNodeNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get graph node: %w", err)
	}

	return node, nil
}

type nodeRow interface {
	Scan(dest ...any) error
}

func scanNode(row nodeRow) (*Node, error) {
	node := &Node{}
	var properties []byte

	if err := row.Scan(
		&node.ID,
		&node.TeamID,
		&node.Type,
		&node.Status,
		&node.Label,
		&node.ExternalID,
		&properties,
		&node.CreatedAt,
		&node.UpdatedAt,
	); err != nil {
		return nil, err
	}

	node.Properties = properties

	return node, nil
}

func scanEdge(row nodeRow) (*Edge, error) {
	edge := &Edge{}
	var properties []byte

	if err := row.Scan(
		&edge.ID,
		&edge.TeamID,
		&edge.FromNodeID,
		&edge.ToNodeID,
		&edge.Type,
		&edge.Status,
		&edge.Weight,
		&properties,
		&edge.CreatedAt,
		&edge.UpdatedAt,
	); err != nil {
		return nil, err
	}

	edge.Properties = properties

	return edge, nil
}

func scanEdgeAndNode(row nodeRow) (*Edge, *Node, error) {
	edge := &Edge{}
	node := &Node{}
	var edgeProperties []byte
	var nodeProperties []byte

	if err := row.Scan(
		&edge.ID,
		&edge.TeamID,
		&edge.FromNodeID,
		&edge.ToNodeID,
		&edge.Type,
		&edge.Status,
		&edge.Weight,
		&edgeProperties,
		&edge.CreatedAt,
		&edge.UpdatedAt,
		&node.ID,
		&node.TeamID,
		&node.Type,
		&node.Status,
		&node.Label,
		&node.ExternalID,
		&nodeProperties,
		&node.CreatedAt,
		&node.UpdatedAt,
	); err != nil {
		return nil, nil, err
	}

	edge.Properties = edgeProperties
	node.Properties = nodeProperties

	return edge, node, nil
}
