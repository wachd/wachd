import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import GraphSettingsPanel from "./graph-settings-panel";

function envelope(data: unknown): Response {
  return new Response(JSON.stringify({ data, error: null }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

const graphConfig = {
  enabled: true,
  min_similarity_score: 0.12,
};

const graphNode = {
  id: "node-1",
  team_id: "team-1",
  type: "incident",
  status: "permanent",
  label: "Checkout incident",
  created_at: "2026-03-12T09:30:00Z",
  updated_at: "2026-03-12T09:30:00Z",
};

describe("GraphSettingsPanel", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
    vi.spyOn(window, "confirm").mockReturnValue(true);
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("renders graph config controls", async () => {
    fetchMock.mockResolvedValueOnce(envelope(graphConfig));
    fetchMock.mockResolvedValueOnce(envelope([]));

    render(<GraphSettingsPanel teamId="team-1" isAdmin />);

    expect(await screen.findByText("Graph settings")).toBeInTheDocument();
    expect(screen.getByLabelText("Minimum similarity score")).toBeInTheDocument();
    expect(screen.getByText("12%")).toBeInTheDocument();
  });

  it("does not render delete node buttons for non-admin rendering", async () => {
    fetchMock.mockResolvedValueOnce(envelope(graphConfig));

    render(<GraphSettingsPanel teamId="team-1" isAdmin={false} />);

    expect(await screen.findByText("Graph settings")).toBeInTheDocument();
    expect(screen.queryByText("Delete")).not.toBeInTheDocument();

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });
  });

  it("keeps the node visible and shows an error when delete fails", async () => {
    fetchMock.mockResolvedValueOnce(envelope(graphConfig));
    fetchMock.mockResolvedValueOnce(envelope([graphNode]));
    fetchMock.mockResolvedValueOnce(new Response("delete failed", { status: 500 }));

    render(<GraphSettingsPanel teamId="team-1" isAdmin />);

    expect(await screen.findByText("Checkout incident")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Delete" }));

    expect(await screen.findByText("Failed to delete node. Please try again.")).toBeInTheDocument();
    expect(screen.getByText("Checkout incident")).toBeInTheDocument();
  });
});
