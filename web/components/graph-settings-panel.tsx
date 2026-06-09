"use client";

import { useEffect, useState } from "react";

import GraphServicesSection from "@/components/graph-services-section";
import { api } from "@/lib/api";
import type { GraphConfig, GraphNode } from "@/lib/types";

interface GraphSettingsPanelProps {
  teamId: string;
  isAdmin: boolean;
}

const defaultGraphConfig: GraphConfig = {
  enabled: true,
  min_similarity_score: 0.12,
};

function formatPercent(value: number): string {
  return `${Math.round(value * 100)}%`;
}

function formatDateTime(value: string): string {
  return new Date(value).toLocaleString();
}

export default function GraphSettingsPanel({ teamId, isAdmin }: GraphSettingsPanelProps) {
  const [config, setConfig] = useState<GraphConfig>(defaultGraphConfig);
  const [nodes, setNodes] = useState<GraphNode[]>([]);
  const [loading, setLoading] = useState(true);
  const [nodesLoading, setNodesLoading] = useState(isAdmin);
  const [saving, setSaving] = useState(false);
  const [deletingNodeId, setDeletingNodeId] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;

    void Promise.resolve().then(async () => {
      if (cancelled) {
        return;
      }

      setLoading(true);

      try {
        const loadedConfig = await api.graph.getConfig(teamId);

        if (!cancelled) {
          setConfig(loadedConfig ?? defaultGraphConfig);
        }
      } catch {
        if (!cancelled) {
          setConfig(defaultGraphConfig);
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
  }, [teamId]);

  useEffect(() => {
    let cancelled = false;

    void Promise.resolve().then(async () => {
      if (cancelled) {
        return;
      }

      if (!isAdmin) {
        setNodes([]);
        setNodesLoading(false);
        return;
      }

      setNodesLoading(true);

      try {
        const loadedNodes = await api.graph.listNodes(teamId, "permanent", 50);

        if (!cancelled) {
          setNodes(loadedNodes);
        }
      } catch {
        if (!cancelled) {
          setNodes([]);
        }
      } finally {
        if (!cancelled) {
          setNodesLoading(false);
        }
      }
    });

    return () => {
      cancelled = true;
    };
  }, [teamId, isAdmin]);

  const saveConfig = async () => {
    setSaving(true);
    setMessage(null);

    try {
      const updated = await api.graph.updateConfig(teamId, config);
      setConfig(updated);
      setMessage("Graph settings saved.");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Failed to save graph settings.");
    } finally {
      setSaving(false);
    }
  };

  const deleteNode = async (node: GraphNode) => {
    if (!isAdmin) {
      return;
    }

    const confirmed = window.confirm(`Delete graph node "${node.label}"?`);
    if (!confirmed) {
      return;
    }

    setDeletingNodeId(node.id);
    setMessage(null);

    try {
      await api.graph.deleteNode(teamId, node.id);
      setNodes((current) => current.filter((item) => item.id !== node.id));
      setMessage("Graph node deleted.");
    } catch {
      setMessage("Failed to delete node. Please try again.");
    } finally {
      setDeletingNodeId(null);
    }
  };

  if (loading) {
    return (
      <div className="bg-white rounded-lg border border-gray-200 p-6 text-sm text-gray-500">
        Loading graph settings...
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <section className="bg-white rounded-lg border border-gray-200 p-6 space-y-5">
        <div>
          <h2 className="text-lg font-semibold text-gray-900">Graph settings</h2>
          <p className="mt-1 text-sm text-gray-500">
            Control incident similarity matching and knowledge graph behavior for this team.
          </p>
        </div>

        <label className="flex items-start gap-3">
          <input
            type="checkbox"
            checked={config.enabled}
            onChange={(event) =>
              setConfig((current) => ({
                ...current,
                enabled: event.target.checked,
              }))
            }
            className="mt-1"
          />
          <span>
            <span className="block text-sm font-medium text-gray-900">
              Enable similarity matching
            </span>
            <span className="block text-sm text-gray-500">
              When enabled, resolved incidents can be used as memory for future similarity lookup.
            </span>
          </span>
        </label>

        <div>
          <div className="flex items-center justify-between gap-4">
            <label className="text-sm font-medium text-gray-900" htmlFor="min-similarity-score">
              Minimum similarity score
            </label>
            <span className="rounded-full bg-gray-100 px-2 py-1 text-xs font-semibold text-gray-700">
              {formatPercent(config.min_similarity_score)}
            </span>
          </div>

          <input
            id="min-similarity-score"
            type="range"
            min="0.05"
            max="0.50"
            step="0.01"
            value={config.min_similarity_score}
            onChange={(event) =>
              setConfig((current) => ({
                ...current,
                min_similarity_score: Number(event.target.value),
              }))
            }
            className="mt-3 w-full"
          />

          <div className="mt-1 flex justify-between text-xs text-gray-500">
            <span>5%</span>
            <span>50%</span>
          </div>
        </div>

        {message && <p className="text-sm text-gray-600">{message}</p>}

        <button
          type="button"
          onClick={saveConfig}
          disabled={saving}
          className="px-4 py-2 bg-blue-600 text-white rounded-md hover:bg-blue-700 transition-colors disabled:opacity-50 text-sm"
        >
          {saving ? "Saving..." : "Save graph settings"}
        </button>
      </section>

      <GraphServicesSection teamId={teamId} isAdmin={isAdmin} />

      <section className="bg-white rounded-lg border border-gray-200">
        <div className="p-6 border-b border-gray-100">
          <h2 className="text-lg font-semibold text-gray-900">Permanent graph nodes</h2>
          <p className="mt-1 text-sm text-gray-500">
            Permanent knowledge graph nodes currently available for similarity and traversal.
          </p>
        </div>

        {!isAdmin ? (
          <div className="p-6 text-sm text-gray-500">
            Admin access is required to manage graph nodes.
          </div>
        ) : nodesLoading ? (
          <div className="p-6 text-sm text-gray-500">Loading graph nodes...</div>
        ) : nodes.length === 0 ? (
          <div className="p-6 text-sm text-gray-500">No permanent graph nodes found.</div>
        ) : (
          <div className="overflow-x-auto">
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-gray-100">
                  <th className="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
                    Label
                  </th>
                  <th className="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
                    Type
                  </th>
                  <th className="px-6 py-3 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
                    Updated
                  </th>
                  <th className="px-6 py-3" />
                </tr>
              </thead>
              <tbody className="divide-y divide-gray-50">
                {nodes.map((node) => (
                  <tr key={node.id} className="hover:bg-gray-50">
                    <td className="px-6 py-3 font-medium text-gray-900">{node.label}</td>
                    <td className="px-6 py-3 text-gray-600">{node.type}</td>
                    <td className="px-6 py-3 text-gray-600">{formatDateTime(node.updated_at)}</td>
                    <td className="px-6 py-3 text-right">
                      {isAdmin && (
                        <button
                          type="button"
                          onClick={() => void deleteNode(node)}
                          disabled={deletingNodeId === node.id}
                          className="text-xs text-red-600 hover:underline disabled:opacity-50"
                        >
                          {deletingNodeId === node.id ? "Deleting..." : "Delete"}
                        </button>
                      )}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  );
}
