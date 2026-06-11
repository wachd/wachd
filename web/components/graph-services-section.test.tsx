import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import GraphServicesSection from "./graph-services-section";

function envelope(data: unknown): Response {
  return new Response(JSON.stringify({ data, error: null }), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}

const service = {
  id: "service-node-1",
  name: "checkout-api",
  label: "Checkout API",
  description: "Handles checkout flow",
  created_at: "2026-06-01T00:00:00Z",
  updated_at: "2026-06-01T00:00:00Z",
};

describe("GraphServicesSection", () => {
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

  it("renders declared services", async () => {
    fetchMock.mockResolvedValueOnce(envelope([service]));

    render(<GraphServicesSection teamId="team-1" isAdmin />);

    expect(await screen.findByText("checkout-api")).toBeInTheDocument();
    expect(screen.getByText("Checkout API")).toBeInTheDocument();
    expect(screen.getByText("Handles checkout flow")).toBeInTheDocument();
  });

  it("creates a service", async () => {
    fetchMock.mockResolvedValueOnce(envelope([]));
    fetchMock.mockResolvedValueOnce(envelope(service));

    render(<GraphServicesSection teamId="team-1" isAdmin />);

    expect(await screen.findByText("No services declared yet.")).toBeInTheDocument();

    fireEvent.change(screen.getByPlaceholderText("checkout-api"), {
      target: { value: "checkout-api" },
    });
    fireEvent.change(screen.getByPlaceholderText("Checkout API"), {
      target: { value: "Checkout API" },
    });

    fireEvent.click(screen.getByRole("button", { name: "Add service" }));

    expect(await screen.findByText("Service saved.")).toBeInTheDocument();
    expect(screen.getByText("checkout-api")).toBeInTheDocument();
  });

  it("deletes a service after API success", async () => {
    fetchMock.mockResolvedValueOnce(envelope([service]));
    fetchMock.mockResolvedValueOnce(envelope({ deleted: service.id }));

    render(<GraphServicesSection teamId="team-1" isAdmin />);

    expect(await screen.findByText("checkout-api")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Delete" }));

    expect(await screen.findByText("Service deleted.")).toBeInTheDocument();

    await waitFor(() => {
      expect(screen.queryByText("checkout-api")).not.toBeInTheDocument();
    });
  });
});
