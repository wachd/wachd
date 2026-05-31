import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import SimilarIncidentsPanel, { formatSimilarityScore } from "./similar-incidents-panel";

function envelope(data: unknown, status = 200): Response {
  return new Response(JSON.stringify({ data, error: null }), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function makeSimilarIncident(index: number, score = 0.84) {
  return {
    incident_id: `incident-${index}`,
    title: `Payment timeout ${index}`,
    score,
    reason: `similar root cause ${index}`,
    occurred_at: "2026-03-12T09:30:00Z",
    resolution: `rolled back v2.3.${index}`,
  };
}

describe("SimilarIncidentsPanel", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
  });

  it("renders the empty state when there are no similar incidents", async () => {
    fetchMock.mockResolvedValueOnce(envelope([]));

    render(<SimilarIncidentsPanel teamId="team-1" incidentId="incident-current" />);

    expect(screen.getByTestId("similar-incidents-loading")).toBeInTheDocument();

    expect(await screen.findByText("No similar past incidents found")).toBeInTheDocument();
  });

  it("renders one similar incident with a percentage badge", async () => {
    fetchMock.mockResolvedValueOnce(envelope([makeSimilarIncident(1, 0.84)]));

    render(<SimilarIncidentsPanel teamId="team-1" incidentId="incident-current" />);

    expect(await screen.findByText("Payment timeout 1")).toBeInTheDocument();
    expect(screen.getByText("84%")).toBeInTheDocument();
    expect(screen.getByText("similar root cause 1")).toBeInTheDocument();
    expect(screen.getByText(/rolled back v2\.3\.1/)).toBeInTheDocument();
  });

  it("renders five similar incidents", async () => {
    fetchMock.mockResolvedValueOnce(
      envelope(Array.from({ length: 5 }, (_, index) => makeSimilarIncident(index + 1)))
    );

    render(<SimilarIncidentsPanel teamId="team-1" incidentId="incident-current" />);

    await waitFor(() => {
      expect(screen.getAllByRole("link")).toHaveLength(5);
    });

    expect(screen.getByText("Payment timeout 5")).toBeInTheDocument();
  });

  it("hides itself when the API request fails", async () => {
    fetchMock.mockResolvedValueOnce(new Response("backend unavailable", { status: 500 }));

    const { container } = render(
      <SimilarIncidentsPanel teamId="team-1" incidentId="incident-current" />
    );

    await waitFor(() => {
      expect(container.textContent).toBe("");
    });
  });

  it("formats scores as clamped percentages", () => {
    expect(formatSimilarityScore(0.84)).toBe("84%");
    expect(formatSimilarityScore(1.2)).toBe("100%");
    expect(formatSimilarityScore(-0.2)).toBe("0%");
  });
});
