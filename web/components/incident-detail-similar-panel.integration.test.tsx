import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import IncidentDetailPage from "@/app/incidents/[id]/page";

vi.mock("next/navigation", () => ({
  useParams: () => ({ id: "incident-current" }),
  useRouter: () => ({ back: vi.fn() }),
}));

vi.mock("@/lib/session-context", () => ({
  useSession: () => ({
    primaryTeamId: "team-1",
  }),
}));

function jsonResponse(data: unknown): Response {
  return new Response(JSON.stringify(data), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

describe("incident detail similar incidents integration", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchMock = vi.fn((url: string) => {
      if (url.includes("/timeline")) {
        return Promise.resolve(
          jsonResponse({
            data: [],
            error: null,
          })
        );
      }

      if (url.includes("/graph")) {
        return Promise.resolve(
          jsonResponse({
            data: {
              nodes: [],
              edges: [],
            },
            error: null,
          })
        );
      }

      if (url.includes("/similar")) {
        return Promise.resolve(
          jsonResponse({
            data: [
              {
                incident_id: "incident-past",
                title: "Payment timeout",
                score: 0.84,
                reason: "similar root cause; same service: checkout-api",
                occurred_at: "2026-03-12T09:30:00Z",
                resolution: "rolled back v2.3.1",
              },
            ],
            error: null,
          })
        );
      }

      return Promise.resolve(
        jsonResponse({
          id: "incident-current",
          team_id: "team-1",
          title: "Checkout database timeout",
          message: "Checkout is returning 500s",
          severity: "critical",
          status: "open",
          source: "grafana",
          fired_at: "2026-03-13T09:30:00Z",
          alert_payload: {},
          created_at: "2026-03-13T09:30:00Z",
          updated_at: "2026-03-13T09:30:00Z",
        })
      );
    });

    vi.stubGlobal("fetch", fetchMock);
  });

  it("shows the similar incidents panel and links to the past incident", async () => {
    render(<IncidentDetailPage />);

    expect(await screen.findByText("Checkout database timeout")).toBeInTheDocument();
    expect(await screen.findByText("Similar past incidents")).toBeInTheDocument();

    const link = await screen.findByRole("link", { name: /Payment timeout/i });
    expect(link).toHaveAttribute("href", "/incidents/incident-past");
  });
});
