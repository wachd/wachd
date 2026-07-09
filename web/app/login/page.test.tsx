import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest';

import LoginPage from './page';

vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: vi.fn() }),
  useSearchParams: () => ({ get: vi.fn().mockReturnValue(null) }),
}));

describe('LoginPage', () => {
  it('renders the username and password fields', () => {
    render(<LoginPage />);
    expect(screen.getByPlaceholderText('wachd_admin')).toBeDefined();
    expect(screen.getByPlaceholderText('\u2022\u2022\u2022\u2022\u2022\u2022\u2022\u2022')).toBeDefined();
  });

  it('renders the Sign in button', () => {
    render(<LoginPage />);
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeDefined();
  });

  it('shows SSO option', () => {
    render(<LoginPage />);
    expect(screen.getByRole('button', { name: /sign in with microsoft/i })).toBeDefined();
  });

  describe('demo mode hint', () => {
    const originalEnv = process.env;

    beforeEach(() => {
      process.env = { ...originalEnv };
    });

    afterEach(() => {
      process.env = originalEnv;
    });

    it('hides the hint when NEXT_PUBLIC_DEMO_MODE is not set', () => {
      delete process.env.NEXT_PUBLIC_DEMO_MODE;
      render(<LoginPage />);
      expect(screen.queryByText(/using the wachd demo/i)).toBeNull();
    });

    it('hides the hint when NEXT_PUBLIC_DEMO_MODE is false', () => {
      process.env.NEXT_PUBLIC_DEMO_MODE = 'false';
      render(<LoginPage />);
      expect(screen.queryByText(/using the wachd demo/i)).toBeNull();
    });

    it('shows the hint when NEXT_PUBLIC_DEMO_MODE is true', () => {
      process.env.NEXT_PUBLIC_DEMO_MODE = 'true';
      render(<LoginPage />);
      expect(screen.getByText(/using the wachd demo/i)).toBeDefined();
      expect(screen.getByText(/check your email for login credentials/i)).toBeDefined();
    });
  });
});
