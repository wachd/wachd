import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { IncidentsView } from './incidents-view';

vi.mock('@/lib/session-context', () => ({
  useSession: () => ({ primaryTeamId: 'team-123', loading: false }),
}));

function inc(id: string, analysis: Record<string, unknown>) {
  return {
    id, team_id: 'team-123', title: `Incident ${id}`, severity: 'high', status: 'open', source: 'grafana',
    fired_at: '2026-11-27T10:55:14Z', alert_payload: {}, created_at: '2026-11-27T10:55:14Z',
    updated_at: '2026-11-27T10:55:14Z', analysis,
  };
}

vi.mock('@/lib/api', () => ({
  api: {
    incidents: {
      list: vi.fn().mockResolvedValue([
        inc('a', { root_cause: 'Hypothesis text A', evidence_classification: 'INSUFFICIENT_EVIDENCE' }),
        inc('b', { root_cause: 'Probable text B', evidence_classification: 'PROBABLE_ROOT_CAUSE' }),
        inc('c', { root_cause: 'Legacy text C' }),
      ]),
    },
  },
}));

vi.mock('next/link', () => ({
  default: ({ href, children, ...props }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...props}>{children}</a>
  ),
}));

describe('IncidentsView analysis labels', () => {
  it('does not present an unverified hypothesis as a plain analysis', async () => {
    render(<IncidentsView demoMode={false} />);
    const hypo = (await screen.findByText('Hypothesis text A')).closest('p') as HTMLElement;
    expect(hypo).toHaveTextContent('AI hypothesis (unverified):');
    expect(hypo).not.toHaveTextContent('AI Analysis:');

    expect((screen.getByText('Probable text B').closest('p') as HTMLElement)).toHaveTextContent('AI Analysis:');
    expect((screen.getByText('Legacy text C').closest('p') as HTMLElement)).toHaveTextContent('AI Analysis:');
  });
});
