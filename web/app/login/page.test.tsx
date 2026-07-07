import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import LoginPage from './page';

// next/navigation is used inside LoginForm via useRouter/useSearchParams
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

  it('shows a demo credential hint below the Sign in button', () => {
    render(<LoginPage />);
    expect(screen.getByText(/using the wachd demo/i)).toBeDefined();
    expect(screen.getByText(/check your email for login credentials/i)).toBeDefined();
  });

  it('shows SSO option', () => {
    render(<LoginPage />);
    expect(screen.getByRole('button', { name: /sign in with microsoft/i })).toBeDefined();
  });
});
