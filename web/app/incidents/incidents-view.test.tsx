import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { IncidentsView } from './incidents-view';

vi.mock('@/lib/session-context', () => ({
  useSession: () => ({ primaryTeamId: 'team-123', loading: false }),
}));

vi.mock('@/lib/api', () => ({
  api: {
    incidents: {
      list: vi.fn().mockResolvedValue([]),
    },
  },
}));

vi.mock('next/link', () => ({
  default: ({ href, children, ...props }: { href: string; children: React.ReactNode }) => (
    <a href={href} {...props}>{children}</a>
  ),
}));

describe('IncidentsView empty state', () => {
  it('shows self-hosted onboarding checklist when demoMode is false', async () => {
    render(<IncidentsView demoMode={false} />);
    expect(await screen.findByText(/here's how to get your first alert/i)).toBeDefined();
    expect(screen.getByText(/configure notifications/i)).toBeDefined();
    expect(screen.getByText(/set up your on-call schedule/i)).toBeDefined();
  });

  it('shows demo control panel prompt when demoMode is true', async () => {
    render(<IncidentsView demoMode={true} />);
    expect(await screen.findByText(/go back to the demo control panel/i)).toBeDefined();
    expect(screen.getByRole('link', { name: /back to demo control panel/i })).toBeDefined();
  });

  it('does not show self-hosted checklist in demo mode', async () => {
    render(<IncidentsView demoMode={true} />);
    await screen.findByText(/go back to the demo control panel/i);
    expect(screen.queryByText(/configure notifications/i)).toBeNull();
  });
});
