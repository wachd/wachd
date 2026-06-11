"use client";

import { FormEvent, useEffect, useState } from "react";

import { api } from "@/lib/api";
import type { GraphService, GraphServiceInput } from "@/lib/types";

interface GraphServicesSectionProps {
  teamId: string;
  isAdmin: boolean;
}

const emptyServiceInput: GraphServiceInput = {
  name: "",
  label: "",
  description: "",
};

export default function GraphServicesSection({ teamId, isAdmin }: GraphServicesSectionProps) {
  const [services, setServices] = useState<GraphService[]>([]);
  const [form, setForm] = useState<GraphServiceInput>(emptyServiceInput);
  const [loading, setLoading] = useState(isAdmin);
  const [saving, setSaving] = useState(false);
  const [deletingServiceId, setDeletingServiceId] = useState<string | null>(null);
  const [message, setMessage] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;

    void Promise.resolve().then(async () => {
      if (cancelled) {
        return;
      }

      if (!isAdmin) {
        setServices([]);
        setLoading(false);
        return;
      }

      setLoading(true);

      try {
        const loadedServices = await api.graph.listServices(teamId);

        if (!cancelled) {
          setServices(loadedServices);
        }
      } catch {
        if (!cancelled) {
          setServices([]);
          setMessage("Failed to load services.");
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
  }, [teamId, isAdmin]);

  const createService = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();

    const name = form.name.trim();
    if (!name) {
      setMessage("Service name is required.");
      return;
    }

    setSaving(true);
    setMessage(null);

    try {
      const created = await api.graph.createService(teamId, {
        name,
        label: form.label?.trim() || name,
        description: form.description?.trim() || undefined,
      });

      setServices((current) => {
        const remaining = current.filter((service) => service.id !== created.id);
        return [...remaining, created].sort((a, b) => a.name.localeCompare(b.name));
      });
      setForm(emptyServiceInput);
      setMessage("Service saved.");
    } catch (error) {
      setMessage(error instanceof Error ? error.message : "Failed to save service.");
    } finally {
      setSaving(false);
    }
  };

  const deleteService = async (service: GraphService) => {
    if (!isAdmin) {
      return;
    }

    const confirmed = window.confirm(`Delete service "${service.label}"?`);
    if (!confirmed) {
      return;
    }

    setDeletingServiceId(service.id);
    setMessage(null);

    try {
      await api.graph.deleteService(teamId, service.id);
      setServices((current) => current.filter((item) => item.id !== service.id));
      setMessage("Service deleted.");
    } catch {
      setMessage("Failed to delete service. Please try again.");
    } finally {
      setDeletingServiceId(null);
    }
  };

  if (!isAdmin) {
    return null;
  }

  return (
    <section className="bg-white rounded-lg border border-gray-200 p-6 space-y-5">
      <div>
        <h2 className="text-lg font-semibold text-gray-900">Services</h2>
        <p className="mt-1 text-sm text-gray-500">
          Declare services so incidents can link to stable service nodes in the graph.
        </p>
      </div>

      <form className="grid gap-3 md:grid-cols-3" onSubmit={createService}>
        <label className="text-sm">
          <span className="block font-medium text-gray-700">Service name</span>
          <input
            value={form.name}
            onChange={(event) => setForm((current) => ({ ...current, name: event.target.value }))}
            placeholder="checkout-api"
            className="mt-1 w-full rounded-md border border-gray-300 px-3 py-2"
          />
        </label>

        <label className="text-sm">
          <span className="block font-medium text-gray-700">Label</span>
          <input
            value={form.label}
            onChange={(event) => setForm((current) => ({ ...current, label: event.target.value }))}
            placeholder="Checkout API"
            className="mt-1 w-full rounded-md border border-gray-300 px-3 py-2"
          />
        </label>

        <label className="text-sm">
          <span className="block font-medium text-gray-700">Description</span>
          <input
            value={form.description}
            onChange={(event) =>
              setForm((current) => ({ ...current, description: event.target.value }))
            }
            placeholder="Handles checkout flow"
            className="mt-1 w-full rounded-md border border-gray-300 px-3 py-2"
          />
        </label>

        <div className="md:col-span-3">
          <button
            type="submit"
            disabled={saving}
            className="px-4 py-2 bg-blue-600 text-white rounded-md hover:bg-blue-700 transition-colors disabled:opacity-50 text-sm"
          >
            {saving ? "Saving..." : "Add service"}
          </button>
        </div>
      </form>

      {message && <p className="text-sm text-gray-600">{message}</p>}

      {loading ? (
        <p className="text-sm text-gray-500">Loading services...</p>
      ) : services.length === 0 ? (
        <p className="text-sm text-gray-500">No services declared yet.</p>
      ) : (
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-gray-100">
                <th className="px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
                  Name
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
                  Label
                </th>
                <th className="px-3 py-2 text-left text-xs font-medium uppercase tracking-wider text-gray-500">
                  Description
                </th>
                <th className="px-3 py-2" />
              </tr>
            </thead>
            <tbody className="divide-y divide-gray-50">
              {services.map((service) => (
                <tr key={service.id}>
                  <td className="px-3 py-2 font-mono text-xs text-gray-700">{service.name}</td>
                  <td className="px-3 py-2 font-medium text-gray-900">{service.label}</td>
                  <td className="px-3 py-2 text-gray-600">{service.description || "-"}</td>
                  <td className="px-3 py-2 text-right">
                    <button
                      type="button"
                      onClick={() => void deleteService(service)}
                      disabled={deletingServiceId === service.id}
                      className="text-xs text-red-600 hover:underline disabled:opacity-50"
                    >
                      {deletingServiceId === service.id ? "Deleting..." : "Delete"}
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  );
}
