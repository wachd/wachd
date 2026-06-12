import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import IncidentGraphExplorer from "./incident-graph-explorer";

const pushMock = vi.fn();

vi.mock("next/navigation", () => ({
  useRouter: () => ({
    push: pushMock,
  }),
}));

function envelope(data: unknown, status = 200): Response {
  return new Response(JSON.stringify({ data, error: null }), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const graph = {
  nodes: [
    {
      id: "node-current",
      team_id: "team-1",
      type: "incident",
      status: "permanent",
      label: "Checkout outage",
      external_id: "incident-current",
      created_at: "2026-06-01T00:00:00Z",
      updated_at: "2026-06-01T00:00:00Z",
    },
    {
      id: "node-service",
      team_id: "team-1",
      type: "service",
      status: "permanent",
      label: "checkout-api",
      external_id: "checkout-api",
      created_at: "2026-06-01T00:00:00Z",
      updated_at: "2026-06-01T00:00:00Z",
    },
    {
      id: "node-past",
      team_id: "team-1",
      type: "incident",
      status: "permanent",
      label: "Past checkout outage",
      external_id: "incident-past",
      created_at: "2026-06-01T00:00:00Z",
      updated_at: "2026-06-01T00:00:00Z",
    },
  ],
  edges: [
    {
      id: "edge-affects",
      team_id: "team-1",
      from_node_id: "node-current",
      to_node_id: "node-service",
      type: "affects",
      status: "permanent",
      weight: 1,
      created_at: "2026-06-01T00:00:00Z",
      updated_at: "2026-06-01T00:00:00Z",
    },
    {
      id: "edge-similar",
      team_id: "team-1",
      from_node_id: "node-current",
      to_node_id: "node-past",
      type: "similar_to",
      status: "permanent",
      weight: 0.84,
      created_at: "2026-06-01T00:00:00Z",
      updated_at: "2026-06-01T00:00:00Z",
    },
  ],
};

describe("IncidentGraphExplorer", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    pushMock.mockReset();
  });

  it("renders graph nodes and edge labels when edges exist", async () => {
    fetchMock.mockResolvedValueOnce(envelope(graph));

    render(<IncidentGraphExplorer teamId="team-1" incidentId="incident-current" />);

    expect(await screen.findByText("Checkout outage")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Incident graph" })).toBeInTheDocument();
    expect(screen.getByText("checkout-api")).toBeInTheDocument();
    expect(screen.getByText("affects")).toBeInTheDocument();
    expect(screen.getByText("similar_to")).toBeInTheDocument();

    const affectsLine = screen.getByTestId("graph-edge-edge-affects");
    const y2 = Number(affectsLine.getAttribute("y2"));

    expect(y2).toBeGreaterThan(65);
    expect(y2).toBeLessThan(180);
  });

  it("hides itself when the graph has no edges", async () => {
    fetchMock.mockResolvedValueOnce(envelope({ nodes: graph.nodes.slice(0, 1), edges: [] }));

    const { container } = render(
      <IncidentGraphExplorer teamId="team-1" incidentId="incident-current" />
    );

    await waitFor(() => {
      expect(container.textContent).toBe("");
    });
  });

  it("hides itself when the API fails", async () => {
    fetchMock.mockResolvedValueOnce(new Response("failed", { status: 500 }));

    const { container } = render(
      <IncidentGraphExplorer teamId="team-1" incidentId="incident-current" />
    );

    await waitFor(() => {
      expect(container.textContent).toBe("");
    });
  });

  it("does not navigate when the current incident node is clicked", async () => {
    fetchMock.mockResolvedValueOnce(envelope(graph));

    render(<IncidentGraphExplorer teamId="team-1" incidentId="incident-current" />);

    expect(await screen.findByText("Checkout outage")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Open incident Checkout outage" })
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByText("Checkout outage"));

    expect(pushMock).not.toHaveBeenCalled();
  });

  it("navigates when an incident node is clicked", async () => {
    fetchMock.mockResolvedValueOnce(envelope(graph));

    render(<IncidentGraphExplorer teamId="team-1" incidentId="incident-current" />);

    const pastIncident = await screen.findByRole("button", {
      name: "Open incident Past checkout outage",
    });

    fireEvent.click(pastIncident);

    expect(pushMock).toHaveBeenCalledWith("/incidents/incident-past");
  });
});
