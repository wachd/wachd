import type { IncidentAnalysis } from '@/lib/types';

// How an analysis should be presented, derived only from the fields Wachd stored.
// The classification is decided by the backend; this file never upgrades it.
export type RcaTone = 'confirmed' | 'probable' | 'hypothesis' | 'legacy';

export interface RcaAssessment {
  tone: RcaTone;
  /** Text for the classification badge. */
  label: string;
  /** Heading above the root-cause text. */
  causeHeading: string;
  /** True when the root cause is an unverified hypothesis. */
  isHypothesis: boolean;
  /** One-sentence explanation shown with the assessment. */
  explanation: string;
}

export function assessAnalysis(analysis: IncidentAnalysis | undefined): RcaAssessment {
  switch (analysis?.evidence_classification) {
    case 'CONFIRMED_ROOT_CAUSE':
      return {
        tone: 'confirmed',
        label: 'Confirmed root cause',
        causeHeading: 'Root Cause',
        isHypothesis: false,
        explanation: 'Supported by verified evidence from the collected data.',
      };
    case 'PROBABLE_ROOT_CAUSE':
      return {
        tone: 'probable',
        label: 'Probable root cause',
        causeHeading: 'Probable Cause',
        isHypothesis: false,
        explanation:
          'Supported by the collected telemetry, but not independently confirmed. Verify before acting on it.',
      };
    case 'TEMPORAL_CORRELATION_ONLY':
      return {
        tone: 'hypothesis',
        label: 'Timing correlation only',
        causeHeading: 'Suspected Cause (unverified hypothesis)',
        isHypothesis: true,
        explanation:
          'Events were found around the same time, but there is no evidence that one caused the other. This is a hypothesis to investigate, not a diagnosis.',
      };
    case 'INSUFFICIENT_EVIDENCE':
      return {
        tone: 'hypothesis',
        label: 'Insufficient evidence',
        causeHeading: 'Suspected Cause (unverified hypothesis)',
        isHypothesis: true,
        explanation:
          'There is not enough verified evidence to establish a root cause. The text below is an unverified AI hypothesis, not a confirmed finding.',
      };
    default:
      return {
        tone: 'legacy',
        label: 'Not recorded',
        causeHeading: 'Suspected Cause',
        isHypothesis: false,
        explanation:
          'This analysis has no evidence classification (it was created before evidence verification). Treat it as unverified.',
      };
  }
}

/** True when the list/summary views must not present the root cause as settled. */
export function isUnverifiedHypothesis(analysis: IncidentAnalysis | undefined): boolean {
  return assessAnalysis(analysis).isHypothesis;
}

/** Shortens model- or log-derived text for display without changing its meaning. */
export function clip(text: string, max: number): string {
  const t = text.replace(/\s+/g, ' ').trim();
  return t.length > max ? `${t.slice(0, max - 1)}…` : t;
}
