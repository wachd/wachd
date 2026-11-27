import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import RcaAnalysisCard from "./rca-analysis-card";
import { assessAnalysis, clip, isUnverifiedHypothesis } from "@/lib/rca";
import type { IncidentAnalysis } from "@/lib/types";

const base: IncidentAnalysis = {
  root_cause: "user-service v4.2.0 removed a null check.",
  suggested_action: "Roll back user-service.",
  confidence: "low",
  key_findings: ["23% of requests fail", "Missing: commit SHA for v4.2.0"],
  correlations: ["Error spike 2m after the alert onset"],
};

function renderCard(extra: Partial<IncidentAnalysis>) {
  return render(<RcaAnalysisCard analysis={{ ...base, ...extra }} />);
}

describe("RcaAnalysisCard: insufficient evidence", () => {
  it("labels the root cause an unverified hypothesis with an unmistakable alert", () => {
    renderCard({ evidence_classification: "INSUFFICIENT_EVIDENCE", confidence: "low" });

    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent(/not a confirmed root cause/i);
    expect(alert).toHaveTextContent(/not enough verified evidence/i);
    expect(screen.getByRole("heading", { name: /suspected cause \(unverified hypothesis\)/i })).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: /^probable cause$/i })).toBeNull();
    expect(screen.queryByRole("heading", { name: /^root cause$/i })).toBeNull();
  });

  it("states classification and confidence as text, not colour alone", () => {
    renderCard({ evidence_classification: "INSUFFICIENT_EVIDENCE", confidence: "low" });
    expect(screen.getByText("Evidence: Insufficient evidence")).toBeInTheDocument();
    expect(screen.getByText("Confidence: low")).toBeInTheDocument();
  });

  it("marks the suggested action as based on an unverified hypothesis", () => {
    renderCard({ evidence_classification: "INSUFFICIENT_EVIDENCE" });
    expect(screen.getByRole("heading", { name: /suggested action \(based on an unverified hypothesis\)/i })).toBeInTheDocument();
    expect(screen.getByText("Roll back user-service.")).toBeInTheDocument();
  });
});

describe("RcaAnalysisCard: temporal correlation only", () => {
  it("is also presented as a hypothesis", () => {
    renderCard({ evidence_classification: "TEMPORAL_CORRELATION_ONLY", confidence: "medium" });
    expect(screen.getByRole("alert")).toHaveTextContent(/no evidence that one caused the other/i);
    expect(screen.getByText("Evidence: Timing correlation only")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: /^suspected cause \(unverified hypothesis\)$/i })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: /suggested action \(based on an unverified hypothesis\)/i })).toBeInTheDocument();
  });
});

describe("RcaAnalysisCard: probable", () => {
  it("is labelled probable, with no hypothesis alert, and says it is not confirmed", () => {
    renderCard({ evidence_classification: "PROBABLE_ROOT_CAUSE", confidence: "high" });
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByText("Evidence: Probable root cause")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: /^probable cause$/i })).toBeInTheDocument();
    expect(screen.getByRole("note")).toHaveTextContent(/not independently confirmed/i);
    expect(screen.getByRole("heading", { name: /^suggested action$/i })).toBeInTheDocument();
  });
});

describe("RcaAnalysisCard: confirmed", () => {
  it("is labelled confirmed with no warning", () => {
    renderCard({ evidence_classification: "CONFIRMED_ROOT_CAUSE", confidence: "high" });
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByText("Evidence: Confirmed root cause")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: /^root cause$/i })).toBeInTheDocument();
  });
});

describe("RcaAnalysisCard: older incidents without structured fields", () => {
  it("renders everything it has, without crashing, and says the classification is missing", () => {
    renderCard({ evidence_classification: undefined, confidence: undefined });
    expect(screen.getByText("user-service v4.2.0 removed a null check.")).toBeInTheDocument();
    expect(screen.getByText("Roll back user-service.")).toBeInTheDocument();
    expect(screen.getByText("Error spike 2m after the alert onset")).toBeInTheDocument();
    expect(screen.getByText("Evidence: Not recorded")).toBeInTheDocument();
    expect(screen.getByText("Confidence: not stated")).toBeInTheDocument();
    expect(screen.getByRole("note")).toHaveTextContent(/created before evidence verification/i);
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByRole("heading", { name: /^suspected cause$/i })).toBeInTheDocument();
  });

  it("treats an unrecognised classification like a missing one", () => {
    renderCard({ evidence_classification: "SOMETHING_NEW" });
    expect(screen.getByText("Evidence: Not recorded")).toBeInTheDocument();
  });

  it("works with only a root cause", () => {
    render(<RcaAnalysisCard analysis={{ root_cause: "Disk full." }} />);
    expect(screen.getByText("Disk full.")).toBeInTheDocument();
    expect(screen.queryByText(/Key Findings/i)).toBeNull();
    expect(screen.queryByText(/Verification notes/i)).toBeNull();
  });
});

describe("RcaAnalysisCard: verification warnings and rejected evidence", () => {
  const withNotes: Partial<IncidentAnalysis> = {
    evidence_classification: "INSUFFICIENT_EVIDENCE",
    key_findings: [
      "23% of requests fail",
      "Classification lowered from PROBABLE_ROOT_CAUSE to INSUFFICIENT_EVIDENCE.",
    ],
    warnings: ["Classification lowered from PROBABLE_ROOT_CAUSE to INSUFFICIENT_EVIDENCE."],
    rejected_evidence: ['log "NullPointerException in getDefaultLocale"', 'metric "api-gateway 5xx rate"'],
    evidence: [
      { type: "metric", ref: "db_pool_in_use", verified: true },
      { type: "log", ref: "made up line", verified: false },
    ],
  };

  it("shows warnings and a readable rejected-evidence summary", () => {
    renderCard(withNotes);
    const notes = screen.getByRole("heading", { name: /verification notes/i }).parentElement as HTMLElement;
    expect(within(notes).getByText(/Classification lowered from PROBABLE_ROOT_CAUSE/)).toBeInTheDocument();
    expect(screen.getByText(/Evidence Wachd could not verify \(2\)/)).toBeInTheDocument();
    expect(screen.getByText(/NullPointerException in getDefaultLocale/)).toBeInTheDocument();
    expect(screen.getByText(/were not found in the data Wachd collected, so they were ignored/i)).toBeInTheDocument();
  });

  it("does not list a verification note twice (once as a finding, once as a warning)", () => {
    renderCard(withNotes);
    expect(screen.getAllByText(/Classification lowered from PROBABLE_ROOT_CAUSE/)).toHaveLength(1);
    expect(screen.getByText("23% of requests fail")).toBeInTheDocument();
  });

  it("lists only verified evidence as verified", () => {
    renderCard(withNotes);
    expect(screen.getByText(/Verified evidence \(1\)/)).toBeInTheDocument();
    expect(screen.getByText("db_pool_in_use")).toBeInTheDocument();
    expect(screen.queryByText("made up line")).toBeNull();
  });

  it("renders hostile text as plain text, never as markup", () => {
    renderCard({
      evidence_classification: "INSUFFICIENT_EVIDENCE",
      rejected_evidence: ['<img src=x onerror="alert(1)"> <script>alert(2)</script>'],
      warnings: ["<b>bold</b> warning"],
    });
    const card = screen.getByTestId("rca-card");
    expect(card.querySelector("script")).toBeNull();
    expect(card.querySelector("img")).toBeNull();
    expect(card.querySelector("b")).toBeNull();
    expect(card).toHaveTextContent("<b>bold</b> warning");
  });

  it("clips very long entries and caps long lists", () => {
    const longRef = "x".repeat(500);
    renderCard({
      evidence_classification: "INSUFFICIENT_EVIDENCE",
      rejected_evidence: [longRef, ...Array.from({ length: 14 }, (_, i) => `ref-${i}`)],
    });
    expect(screen.queryByText(longRef)).toBeNull();
    expect(screen.getByText(/^x{50,}…$/)).toBeInTheDocument();
    expect(screen.getByText("and 5 more")).toBeInTheDocument();
  });
});

describe("RcaAnalysisCard: commits are not deployments", () => {
  it("never says a commit was deployed, and states there is no deployment record", () => {
    renderCard({
      evidence_classification: "PROBABLE_ROOT_CAUSE",
      is_deployment_cause: true,
      causal_commit: "da3db37aaaaaaaaaaaa",
    });
    const card = screen.getByTestId("rca-card");
    expect(card).toHaveTextContent("Suspected change: commit da3db37");
    expect(card).toHaveTextContent(/no deployment record/i);
    expect(card).toHaveTextContent(/cannot confirm that this change was released/i);
    expect(card.textContent).not.toMatch(/was deployed|were deployed|deployed to production/i);
  });

  it("shows no change box when no change is suspected", () => {
    renderCard({ evidence_classification: "INSUFFICIENT_EVIDENCE", is_deployment_cause: false });
    expect(screen.queryByText(/Suspected change/)).toBeNull();
  });
});

describe("rca helpers", () => {
  it("maps each classification and never upgrades one", () => {
    expect(assessAnalysis({ evidence_classification: "CONFIRMED_ROOT_CAUSE" }).tone).toBe("confirmed");
    expect(assessAnalysis({ evidence_classification: "PROBABLE_ROOT_CAUSE" }).isHypothesis).toBe(false);
    expect(assessAnalysis({ evidence_classification: "TEMPORAL_CORRELATION_ONLY" }).isHypothesis).toBe(true);
    expect(assessAnalysis({ evidence_classification: "INSUFFICIENT_EVIDENCE" }).isHypothesis).toBe(true);
    expect(assessAnalysis(undefined).tone).toBe("legacy");
    expect(assessAnalysis({}).isHypothesis).toBe(false);
  });

  it("flags only hypotheses for the list view", () => {
    expect(isUnverifiedHypothesis({ evidence_classification: "INSUFFICIENT_EVIDENCE" })).toBe(true);
    expect(isUnverifiedHypothesis({ evidence_classification: "PROBABLE_ROOT_CAUSE" })).toBe(false);
    expect(isUnverifiedHypothesis(undefined)).toBe(false);
  });

  it("clips text on word-agnostic length and collapses whitespace", () => {
    expect(clip("a   b\n c", 50)).toBe("a b c");
    expect(clip("abcdef", 4)).toBe("abc…");
  });
});
