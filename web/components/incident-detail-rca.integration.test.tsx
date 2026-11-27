import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import IncidentDetailPage from "@/app/incidents/[id]/page";

vi.mock("next/navigation", () => ({
  useParams: () => ({ id: "incident-current" }),
  useRouter: () => ({ back: vi.fn() }),
}));

vi.mock("@/lib/session-context", () => ({
  useSession: () => ({ primaryTeamId: "team-1" }),
}));

function json(data: unknown): Response {
  return new Response(JSON.stringify(data), { status: 200, headers: { "Content-Type": "application/json" } });
}

function incident(analysis: Record<string, unknown>) {
  return {
    id: "incident-current",
    team_id: "team-1",
    title: "api-gateway 5xx error rate 23%",
    message: "5xx after canary",
    severity: "critical",
    status: "open",
    source: "grafana",
    fired_at: "2026-11-27T10:55:14Z",
    alert_payload: {},
    created_at: "2026-11-27T10:55:14Z",
    updated_at: "2026-11-27T10:55:20Z",
    analysis,
  };
}

function serve(inc: unknown) {
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string) => {
      if (url.includes("/timeline")) return Promise.resolve(json({ data: [], error: null }));
      if (url.includes("/graph")) return Promise.resolve(json({ data: { nodes: [], edges: [] }, error: null }));
      if (url.includes("/similar")) return Promise.resolve(json({ data: [], error: null }));
      return Promise.resolve(json(inc));
    })
  );
}

describe("incident detail: RCA uncertainty", () => {
  beforeEach(() => vi.unstubAllGlobals());

  // The shape the v0.4.37 demo smoke test returned.
  it("shows an insufficient-evidence RCA as an unverified hypothesis with its warnings", async () => {
    serve(
      incident({
        root_cause: "user-service v4.2.0 removed a null check.",
        suggested_action: "Execute immediate rollback.",
        confidence: "low",
        evidence_classification: "INSUFFICIENT_EVIDENCE",
        is_deployment_cause: false,
        key_findings: ["Missing: commit SHA for v4.2.0", "A deployment cause was not accepted: no collected pre-onset commit with a patch supports it."],
        warnings: ["A deployment cause was not accepted: no collected pre-onset commit with a patch supports it."],
        rejected_evidence: ['log "NullPointerException in UserPreferencesService"', 'metric "api-gateway 5xx rate"'],
        correlations: ["Alert fired"],
      })
    );
    render(<IncidentDetailPage />);

    expect(await screen.findByRole("alert")).toHaveTextContent(/not a confirmed root cause/i);
    expect(screen.getByText("Evidence: Insufficient evidence")).toBeInTheDocument();
    expect(screen.getByText("Confidence: low")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: /suspected cause \(unverified hypothesis\)/i })).toBeInTheDocument();
    expect(screen.getByText("user-service v4.2.0 removed a null check.")).toBeInTheDocument();
    expect(screen.getByText("Execute immediate rollback.")).toBeInTheDocument();
    expect(screen.getByText("Missing: commit SHA for v4.2.0")).toBeInTheDocument();
    expect(screen.getByText("Alert fired")).toBeInTheDocument();
    expect(screen.getByText(/Evidence Wachd could not verify \(2\)/)).toBeInTheDocument();
    expect(screen.getAllByText(/A deployment cause was not accepted/)).toHaveLength(1);
  });

  it("still renders an incident analysed before structured fields existed", async () => {
    serve(incident({ root_cause: "Disk filled up.", suggested_action: "Expand the volume.", confidence: "high" }));
    render(<IncidentDetailPage />);

    expect(await screen.findByText("Disk filled up.")).toBeInTheDocument();
    expect(screen.getByText("Expand the volume.")).toBeInTheDocument();
    expect(screen.getByText("Evidence: Not recorded")).toBeInTheDocument();
    expect(screen.getByText("Confidence: high")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("shows no RCA card while only the preliminary timeline exists", async () => {
    serve(incident({ summary: "Preliminary timeline", correlations: ["x"] }));
    render(<IncidentDetailPage />);
    expect(await screen.findByText("api-gateway 5xx error rate 23%")).toBeInTheDocument();
    expect(screen.queryByTestId("rca-card")).toBeNull();
  });
});
