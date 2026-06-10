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
	"context"
	"errors"
	"log"

	"github.com/google/uuid"
	"github.com/wachd/wachd/internal/graph"
)

func writeAffectsServiceEdge(ctx context.Context, graphStore graph.Store, teamID uuid.UUID, incidentNodeID uuid.UUID, serviceName string) {
	if graphStore == nil || teamID == uuid.Nil || incidentNodeID == uuid.Nil {
		return
	}

	name := graph.NormalizeServiceName(serviceName)
	if name == "" {
		return
	}

	serviceNode, err := graphStore.FindNodeByExternalID(ctx, teamID, graph.NodeTypeService, name)
	if err != nil {
		if !errors.Is(err, graph.ErrNodeNotFound) {
			log.Printf("warn: find service graph node for team %s service %s: %v", teamID, name, err)
		}
		return
	}

	if serviceNode == nil || serviceNode.ID == uuid.Nil {
		return
	}

	edge := &graph.Edge{
		FromNodeID: incidentNodeID,
		ToNodeID:   serviceNode.ID,
		Type:       graph.EdgeTypeAffects,
		Status:     graph.EdgeStatusPermanent,
		Weight:     1,
	}

	if _, err := graphStore.UpsertEdge(ctx, teamID, edge); err != nil {
		log.Printf("warn: upsert affects edge for team %s service %s: %v", teamID, name, err)
	}
}
