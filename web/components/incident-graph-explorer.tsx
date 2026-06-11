"use client";

import { useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";

import { api } from "@/lib/api";
import type { GraphEdge, GraphNode, IncidentGraph } from "@/lib/types";

interface IncidentGraphExplorerProps {
  teamId: string;
  incidentId: string;
}

interface PositionedNode extends GraphNode {
  x: number;
  y: number;
}

const width = 760;
const height = 360;
const centerX = width / 2;
const centerY = height / 2;

function nodeColor(type: string): string {
  switch (type) {
    case "incident":
      return "#fee2e2";
    case "service":
      return "#dbeafe";
    case "deployment":
      return "#e5e7eb";
    default:
      return "#f3f4f6";
  }
}

function nodeStroke(type: string, highlighted: boolean): string {
  if (highlighted) {
    return "#111827";
  }

  switch (type) {
    case "incident":
      return "#dc2626";
    case "service":
      return "#2563eb";
    case "deployment":
      return "#6b7280";
    default:
      return "#9ca3af";
  }
}

function edgeMidpoint(from: PositionedNode, to: PositionedNode) {
  return {
    x: (from.x + to.x) / 2,
    y: (from.y + to.y) / 2,
  };
}

function layoutGraph(graph: IncidentGraph, incidentId: string): PositionedNode[] {
  const nodes = graph.nodes ?? [];
  if (nodes.length === 0) {
    return [];
  }

  const currentIndex = nodes.findIndex(
    (node) => node.type === "incident" && node.external_id === incidentId
  );

  const ordered = [...nodes];
  if (currentIndex > 0) {
    const [current] = ordered.splice(currentIndex, 1);
    ordered.unshift(current);
  }

  if (ordered.length === 1) {
    return [{ ...ordered[0], x: centerX, y: centerY }];
  }

  return ordered.map((node, index) => {
    if (index === 0 && node.type === "incident" && node.external_id === incidentId) {
      return { ...node, x: centerX, y: centerY };
    }

    const ringIndex = index - 1;
    const ringCount = ordered.length - 1;
    const angle = (2 * Math.PI * ringIndex) / Math.max(ringCount, 1) - Math.PI / 2;

    return {
      ...node,
      x: centerX + Math.cos(angle) * 250,
      y: centerY + Math.sin(angle) * 115,
    };
  });
}

export default function IncidentGraphExplorer({
  teamId,
  incidentId,
}: IncidentGraphExplorerProps) {
  const router = useRouter();
  const [graph, setGraph] = useState<IncidentGraph | null>(null);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let cancelled = false;

    void Promise.resolve().then(async () => {
      if (cancelled) {
        return;
      }

      setLoading(true);
      setFailed(false);

      try {
        const loadedGraph = await api.incidents.subgraph(teamId, incidentId);

        if (!cancelled) {
          setGraph(loadedGraph);
        }
      } catch {
        if (!cancelled) {
          setFailed(true);
          setGraph(null);
        }
      } finally {
        if (!cancelled) {
          setLoading(false);
        }
      }
    });

    return () => {
      cancelled = true;
    };
  }, [teamId, incidentId]);

  const positionedNodes = useMemo(() => {
    if (!graph) {
      return [];
    }

    return layoutGraph(graph, incidentId);
  }, [graph, incidentId]);

  const nodeByID = useMemo(() => {
    return new Map(positionedNodes.map((node) => [node.id, node]));
  }, [positionedNodes]);

  if (failed) {
    return null;
  }

  if (loading) {
    return (
      <section
        className="bg-white rounded-lg border border-gray-200 p-6"
        aria-label="Incident graph"
      >
        <h2 className="text-lg font-semibold text-gray-900">Incident graph</h2>
        <div className="mt-4 h-64 animate-pulse rounded-lg bg-gray-100" />
      </section>
    );
  }

  if (!graph || !graph.edges || graph.edges.length === 0) {
    return null;
  }

  return (
    <section
      className="bg-white rounded-lg border border-gray-200 p-6"
      aria-label="Incident graph"
    >
      <div className="mb-4">
        <h2 className="text-lg font-semibold text-gray-900">Incident graph</h2>
        <p className="mt-1 text-sm text-gray-500">
          Related incidents, services, deployments, and graph relationships.
        </p>
      </div>

      <div className="overflow-x-auto rounded-lg border border-gray-100 bg-gray-50">
        <svg
          role="img"
          aria-label="Incident graph visualization"
          viewBox={`0 0 ${width} ${height}`}
          className="min-w-[760px] w-full h-[360px]"
        >
          <defs>
            <marker
              id="incident-graph-arrow"
              markerWidth="8"
              markerHeight="8"
              refX="7"
              refY="4"
              orient="auto"
              markerUnits="strokeWidth"
            >
              <path d="M 0 0 L 8 4 L 0 8 z" fill="#6b7280" />
            </marker>
          </defs>

          {graph.edges.map((edge: GraphEdge) => {
            const from = nodeByID.get(edge.from_node_id);
            const to = nodeByID.get(edge.to_node_id);
            if (!from || !to) {
              return null;
            }

            const midpoint = edgeMidpoint(from, to);

            return (
              <g key={edge.id}>
                <line
                  x1={from.x}
                  y1={from.y}
                  x2={to.x}
                  y2={to.y}
                  stroke="#9ca3af"
                  strokeWidth="2"
                  markerEnd="url(#incident-graph-arrow)"
                />
                <rect
                  x={midpoint.x - 45}
                  y={midpoint.y - 11}
                  width="90"
                  height="22"
                  rx="11"
                  fill="#ffffff"
                  stroke="#e5e7eb"
                />
                <text
                  x={midpoint.x}
                  y={midpoint.y + 4}
                  textAnchor="middle"
                  className="fill-gray-600 text-[11px]"
                >
                  {edge.type}
                </text>
              </g>
            );
          })}

          {positionedNodes.map((node) => {
            const highlighted = node.type === "incident" && node.external_id === incidentId;
            const clickable = node.type === "incident" && Boolean(node.external_id);

            const content = (
              <>
                <circle
                  cx={node.x}
                  cy={node.y}
                  r={highlighted ? 42 : 34}
                  fill={nodeColor(node.type)}
                  stroke={nodeStroke(node.type, highlighted)}
                  strokeWidth={highlighted ? 4 : 2}
                />
                <text
                  x={node.x}
                  y={node.y - 4}
                  textAnchor="middle"
                  className="fill-gray-900 text-[12px] font-semibold"
                >
                  {node.label.length > 22 ? `${node.label.slice(0, 19)}...` : node.label}
                </text>
                <text
                  x={node.x}
                  y={node.y + 13}
                  textAnchor="middle"
                  className="fill-gray-500 text-[10px]"
                >
                  {node.type}
                </text>
              </>
            );

            if (!clickable) {
              return <g key={node.id}>{content}</g>;
            }

            return (
              <g
                key={node.id}
                role="button"
                tabIndex={0}
                aria-label={`Open incident ${node.label}`}
                className="cursor-pointer"
                onClick={() => router.push(`/incidents/${node.external_id}`)}
                onKeyDown={(event) => {
                  if (event.key === "Enter" || event.key === " ") {
                    router.push(`/incidents/${node.external_id}`);
                  }
                }}
              >
                {content}
              </g>
            );
          })}
        </svg>
      </div>
    </section>
  );
}
