import { assessAnalysis, clip } from '@/lib/rca';
import type { IncidentAnalysis } from '@/lib/types';

const MAX_LISTED = 10;
const MAX_REF_CHARS = 120;
const MAX_NOTE_CHARS = 400;

const TONE_BADGE: Record<string, string> = {
  confirmed: 'bg-green-100 text-green-800',
  probable: 'bg-blue-100 text-blue-800',
  hypothesis: 'bg-yellow-100 text-yellow-900',
  legacy: 'bg-gray-100 text-gray-800',
};

const CONFIDENCE_BADGE: Record<string, string> = {
  high: 'bg-green-100 text-green-800',
  medium: 'bg-yellow-100 text-yellow-800',
  low: 'bg-gray-100 text-gray-800',
};

function List({ items, max }: { items: string[]; max: number }) {
  const shown = items.slice(0, MAX_LISTED);
  return (
    <>
      <ul className="list-disc list-inside space-y-1 text-gray-900">
        {shown.map((item, idx) => (
          <li key={idx} className="text-sm">
            {clip(item, max)}
          </li>
        ))}
      </ul>
      {items.length > shown.length && (
        <p className="text-xs text-gray-500 mt-1">and {items.length - shown.length} more</p>
      )}
    </>
  );
}

// Root-cause analysis for an incident. The classification and warnings come from
// the backend's evidence verification; this component only presents them and
// never upgrades a claim. All model-derived text is rendered as plain text and
// clipped, never as markup.
export default function RcaAnalysisCard({ analysis }: { analysis: IncidentAnalysis }) {
  const view = assessAnalysis(analysis);
  const warnings = analysis.warnings ?? [];
  // The backend also appends its verification notes to key_findings; show each once.
  const findings = (analysis.key_findings ?? []).filter((f) => !warnings.includes(f));
  const rejected = analysis.rejected_evidence ?? [];
  const verified = (analysis.evidence ?? []).filter((e) => e.verified);
  const confidence = analysis.confidence;
  const change = analysis.causal_commit || analysis.is_deployment_cause;

  return (
    <div className="bg-white rounded-lg border border-gray-200 p-6" data-testid="rca-card">
      <h2 className="text-lg font-semibold text-gray-900 mb-4 flex items-center gap-2">
        <span aria-hidden="true">🤖</span>
        Root Cause Analysis
      </h2>

      <div className="flex flex-wrap items-center gap-2 mb-4">
        <span
          className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${TONE_BADGE[view.tone]}`}
        >
          Evidence: {view.label}
        </span>
        <span
          className={`inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${
            confidence ? CONFIDENCE_BADGE[confidence] ?? 'bg-gray-100 text-gray-800' : 'bg-gray-100 text-gray-800'
          }`}
        >
          Confidence: {confidence ?? 'not stated'}
        </span>
      </div>

      {view.isHypothesis ? (
        <div
          role="alert"
          className="mb-4 bg-yellow-50 border border-yellow-300 rounded-md p-3 text-sm text-yellow-900"
        >
          <p className="font-semibold">Unverified assessment — not a confirmed root cause</p>
          <p className="mt-1">{view.explanation}</p>
        </div>
      ) : (
        <p
          role="note"
          className={`mb-4 rounded-md p-3 text-sm ${
            view.tone === 'legacy'
              ? 'bg-gray-50 border border-gray-200 text-gray-700'
              : 'bg-blue-50 border border-blue-200 text-blue-900'
          }`}
        >
          {view.explanation}
        </p>
      )}

      <div className="space-y-4">
        <div>
          <h3 className="text-sm font-medium text-gray-700 mb-1">{view.causeHeading}</h3>
          <p className="text-gray-900">{analysis.root_cause}</p>
        </div>

        {change && (
          <div role="note" className="text-sm bg-gray-50 border border-gray-200 rounded-md p-3 text-gray-800">
            <p className="font-medium">
              Suspected change
              {analysis.causal_commit ? `: commit ${analysis.causal_commit.slice(0, 7)}` : ''}
            </p>
            <p className="mt-1">
              A change in the repository history lines up with this incident. Wachd has no deployment
              record, so it cannot confirm that this change was released.
            </p>
          </div>
        )}

        {analysis.suggested_action && (
          <div>
            <h3 className="text-sm font-medium text-gray-700 mb-1">
              {view.isHypothesis ? 'Suggested Action (based on an unverified hypothesis)' : 'Suggested Action'}
            </h3>
            <p className="text-gray-900">{analysis.suggested_action}</p>
          </div>
        )}

        {findings.length > 0 && (
          <div>
            <h3 className="text-sm font-medium text-gray-700 mb-1">Key Findings</h3>
            <List items={findings} max={MAX_NOTE_CHARS} />
            <p className="text-xs text-gray-500 mt-1">
              Findings are the AI&apos;s statements. Only items under &ldquo;Verified evidence&rdquo; were
              checked against the collected data.
            </p>
          </div>
        )}

        {verified.length > 0 && (
          <div>
            <h3 className="text-sm font-medium text-gray-700 mb-1">Verified evidence ({verified.length})</h3>
            <ul className="space-y-1 text-sm text-gray-900">
              {verified.slice(0, MAX_LISTED).map((e, idx) => (
                <li key={idx}>
                  <span className="text-gray-500">{clip(e.type, 20)}:</span>{' '}
                  <span className="font-mono">{clip(e.ref, MAX_REF_CHARS)}</span>
                </li>
              ))}
            </ul>
          </div>
        )}

        {(warnings.length > 0 || rejected.length > 0) && (
          <div className="border-t border-gray-200 pt-4">
            <h3 className="text-sm font-medium text-gray-700 mb-2">Verification notes</h3>
            {warnings.length > 0 && <List items={warnings} max={MAX_NOTE_CHARS} />}
            {rejected.length > 0 && (
              <details className="mt-2">
                <summary className="text-sm text-gray-700 cursor-pointer">
                  Evidence Wachd could not verify ({rejected.length})
                </summary>
                <p className="text-xs text-gray-500 mt-1">
                  The AI cited these, but they were not found in the data Wachd collected, so they were
                  ignored.
                </p>
                <List items={rejected} max={MAX_REF_CHARS} />
              </details>
            )}
          </div>
        )}

        {analysis.correlations && analysis.correlations.length > 0 && (
          <div>
            <h3 className="text-sm font-medium text-gray-700 mb-2">Timeline Correlations</h3>
            <ul className="list-disc list-inside space-y-1 text-gray-900">
              {analysis.correlations.map((corr, idx) => (
                <li key={idx} className="text-sm">
                  {corr}
                </li>
              ))}
            </ul>
          </div>
        )}
      </div>
    </div>
  );
}
