"use client";

import { useEffect, useState } from "react";
import Link from "next/link";

import { api } from "@/lib/api";
import type { SimilarIncident } from "@/lib/types";

interface SimilarIncidentsPanelProps {
  teamId: string;
  incidentId: string;
}

export function formatSimilarityScore(score: number): string {
  const clamped = Math.min(1, Math.max(0, score));
  return `${Math.round(clamped * 100)}%`;
}

function formatIncidentDate(value?: string): string {
  if (!value) {
    return "";
  }

  return new Date(value).toLocaleDateString(undefined, {
    month: "short",
    day: "numeric",
  });
}

function SimilarIncidentsSkeleton() {
  return (
    <div className="space-y-3" data-testid="similar-incidents-loading">
      {[0, 1, 2].map((index) => (
        <div key={index} className="animate-pulse rounded-lg border border-gray-100 p-4">
          <div className="mb-3 h-4 w-2/3 rounded bg-gray-200" />
          <div className="h-3 w-full rounded bg-gray-100" />
        </div>
      ))}
    </div>
  );
}

export default function SimilarIncidentsPanel({
  teamId,
  incidentId,
}: SimilarIncidentsPanelProps) {
  const [items, setItems] = useState<SimilarIncident[]>([]);
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
        const results = await api.graph.listSimilarIncidents(teamId, incidentId, 5);

        if (!cancelled) {
          setItems(results);
        }
      } catch {
        if (!cancelled) {
          setFailed(true);
          setItems([]);
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

  if (failed) {
    return null;
  }

  return (
    <section className="bg-white rounded-lg border border-gray-200 p-6" aria-label="Similar past incidents">
      <div className="mb-4">
        <h2 className="text-lg font-semibold text-gray-900">Similar past incidents</h2>
        <p className="mt-1 text-sm text-gray-500">
          Previous incidents that look similar to this alert.
        </p>
      </div>

      {loading ? (
        <SimilarIncidentsSkeleton />
      ) : items.length === 0 ? (
        <p className="text-sm text-gray-500">No similar past incidents found</p>
      ) : (
        <div className="divide-y divide-gray-100">
          {items.map((incident) => (
            <Link
              key={incident.incident_id}
              href={`/incidents/${incident.incident_id}`}
              className="group block py-4 first:pt-0 last:pb-0 hover:bg-gray-50"
            >
              <div className="flex items-start gap-3">
                <span className="mt-0.5 inline-flex min-w-12 justify-center rounded-full bg-blue-50 px-2 py-1 text-xs font-semibold text-blue-700">
                  {formatSimilarityScore(incident.score)}
                </span>

                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-baseline justify-between gap-2">
                    <h3 className="font-medium text-gray-900 group-hover:text-blue-700">
                      {incident.title}
                    </h3>
                    {incident.occurred_at && (
                      <span className="text-xs text-gray-500">
                        {formatIncidentDate(incident.occurred_at)}
                      </span>
                    )}
                  </div>

                  {incident.reason && (
                    <p className="mt-1 text-sm text-gray-600">{incident.reason}</p>
                  )}

                  {incident.resolution && (
                    <p className="mt-1 text-sm text-gray-700">
                      <span className="font-medium">Resolution:</span>{" "}
                      {incident.resolution}
                    </p>
                  )}
                </div>

                <span className="pt-1 text-sm text-gray-400 group-hover:text-blue-600">
                  View
                </span>
              </div>
            </Link>
          ))}
        </div>
      )}
    </section>
  );
}
