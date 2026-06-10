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

package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/wachd/wachd/internal/graph"
)

type graphServiceRequest struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

type graphServiceResponse struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Label       string    `json:"label"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func graphServiceProperties(name, description string) json.RawMessage {
	properties := map[string]string{
		"name": name,
	}

	description = strings.TrimSpace(description)
	if description != "" {
		properties["description"] = description
	}

	raw, _ := json.Marshal(properties)
	return raw
}

func graphServiceDescription(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var properties struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &properties); err != nil {
		return ""
	}

	return strings.TrimSpace(properties.Description)
}

func graphServiceResponseFromNode(node *graph.Node) graphServiceResponse {
	name := ""
	if node.ExternalID != nil {
		name = graph.NormalizeServiceName(*node.ExternalID)
	}

	return graphServiceResponse{
		ID:          node.ID.String(),
		Name:        name,
		Label:       node.Label,
		Description: graphServiceDescription(node.Properties),
		CreatedAt:   node.CreatedAt,
		UpdatedAt:   node.UpdatedAt,
	}
}

func listGraphServiceNodes(r *http.Request, graphStore graph.Store, teamID uuid.UUID) ([]*graph.Node, error) {
	nodes, err := graphStore.ListNodes(r.Context(), teamID, graph.NodeStatusPermanent, 200)
	if err != nil {
		return nil, err
	}

	services := make([]*graph.Node, 0)
	for _, node := range nodes {
		if node != nil && node.Type == graph.NodeTypeService {
			services = append(services, node)
		}
	}

	return services, nil
}

func (s *Server) handleListGraphServices(w http.ResponseWriter, r *http.Request) {
	teamID, err := uuid.Parse(mux.Vars(r)["teamId"])
	if err != nil {
		http.Error(w, "invalid team ID", http.StatusBadRequest)
		return
	}

	if !s.requireTeamAccess(r, teamID) {
		writeForbidden(w)
		return
	}

	graphStore, err := s.graphStoreForTeam(r.Context(), teamID)
	if err != nil {
		http.Error(w, "failed to load graph store", http.StatusInternalServerError)
		return
	}

	nodes, err := listGraphServiceNodes(r, graphStore, teamID)
	if err != nil {
		http.Error(w, "failed to list services", http.StatusInternalServerError)
		return
	}

	response := make([]graphServiceResponse, 0, len(nodes))
	for _, node := range nodes {
		response = append(response, graphServiceResponseFromNode(node))
	}

	writeDataEnvelope(w, http.StatusOK, response)
}

func (s *Server) handleCreateGraphService(w http.ResponseWriter, r *http.Request) {
	teamID, err := uuid.Parse(mux.Vars(r)["teamId"])
	if err != nil {
		http.Error(w, "invalid team ID", http.StatusBadRequest)
		return
	}

	if !s.requireTeamAdmin(r, teamID) {
		writeForbidden(w)
		return
	}

	var input graphServiceRequest
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	name := graph.NormalizeServiceName(input.Name)
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	label := strings.TrimSpace(input.Label)
	if label == "" {
		label = name
	}

	graphStore, err := s.graphStoreForTeam(r.Context(), teamID)
	if err != nil {
		http.Error(w, "failed to load graph store", http.StatusInternalServerError)
		return
	}

	statusCode := http.StatusCreated

	// POST is idempotent for the same normalized service name. A repeated POST
	// updates the service label/properties and returns 200 instead of creating a
	// duplicate service node.
	existing, err := graphStore.FindNodeByExternalID(r.Context(), teamID, graph.NodeTypeService, name)
	if err == nil && existing != nil {
		statusCode = http.StatusOK
	} else if err != nil && !errors.Is(err, graph.ErrNodeNotFound) {
		http.Error(w, "failed to load existing service", http.StatusInternalServerError)
		return
	}

	node := &graph.Node{
		Type:       graph.NodeTypeService,
		Status:     graph.NodeStatusPermanent,
		Label:      label,
		ExternalID: &name,
		Properties: graphServiceProperties(name, input.Description),
	}

	created, err := graphStore.UpsertNode(r.Context(), teamID, node)
	if err != nil {
		http.Error(w, "failed to save service", http.StatusInternalServerError)
		return
	}

	writeDataEnvelope(w, statusCode, graphServiceResponseFromNode(created))
}

func (s *Server) handleDeleteGraphService(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)

	teamID, err := uuid.Parse(vars["teamId"])
	if err != nil {
		http.Error(w, "invalid team ID", http.StatusBadRequest)
		return
	}

	nodeID, err := uuid.Parse(vars["nodeId"])
	if err != nil {
		http.Error(w, "invalid node ID", http.StatusBadRequest)
		return
	}

	if !s.requireTeamAdmin(r, teamID) {
		writeForbidden(w)
		return
	}

	graphStore, err := s.graphStoreForTeam(r.Context(), teamID)
	if err != nil {
		http.Error(w, "failed to load graph store", http.StatusInternalServerError)
		return
	}

	nodes, err := listGraphServiceNodes(r, graphStore, teamID)
	if err != nil {
		http.Error(w, "failed to load services", http.StatusInternalServerError)
		return
	}

	found := false
	for _, node := range nodes {
		if node.ID == nodeID {
			found = true
			break
		}
	}
	if !found {
		http.Error(w, "service node not found", http.StatusNotFound)
		return
	}

	if err := graphStore.DeleteNode(r.Context(), teamID, nodeID); err != nil {
		if errors.Is(err, graph.ErrNodeNotFound) {
			http.Error(w, "service node not found", http.StatusNotFound)
			return
		}

		http.Error(w, "failed to delete service", http.StatusInternalServerError)
		return
	}

	writeDataEnvelope(w, http.StatusOK, map[string]string{"deleted": nodeID.String()})
}
