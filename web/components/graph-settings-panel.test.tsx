import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import GraphSettingsPanel from "./graph-settings-panel";

function envelope(data: unknown): Response {
  return new Response(JSON.stringify({ data, error: null }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

describe("GraphSettingsPanel", () => {
  let fetchMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    fetchMock = vi.fn();
    vi.stubGlobal("fetch", fetchMock);
  });

  it("renders graph config controls", async () => {
    fetchMock.mockResolvedValueOnce(
      envelope({
        enabled: true,
        min_similarity_score: 0.12,
      })
    );
    fetchMock.mockResolvedValueOnce(envelope([]));

    render(<GraphSettingsPanel teamId="team-1" isAdmin />);

    expect(await screen.findByText("Graph settings")).toBeInTheDocument();
    expect(screen.getByLabelText("Minimum similarity score")).toBeInTheDocument();
    expect(screen.getByText("12%")).toBeInTheDocument();
  });

  it("does not render delete node buttons for non-admin rendering", async () => {
    fetchMock.mockResolvedValueOnce(
      envelope({
        enabled: true,
        min_similarity_score: 0.12,
      })
    );

    render(<GraphSettingsPanel teamId="team-1" isAdmin={false} />);

    expect(await screen.findByText("Graph settings")).toBeInTheDocument();
    expect(screen.queryByText("Delete")).not.toBeInTheDocument();

    await waitFor(() => {
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });
  });
});
