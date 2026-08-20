import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';

import { LoginForm } from './login-form';

vi.mock('next/navigation', () => ({
  useRouter: () => ({ push: vi.fn() }),
  useSearchParams: () => ({ get: vi.fn().mockReturnValue(null) }),
}));

describe('LoginForm', () => {
  it('renders username and password fields', () => {
    render(<LoginForm demoMode={false} />);
    expect(screen.getByPlaceholderText('wachd_admin')).toBeDefined();
  });

  it('renders the Sign in button', () => {
    render(<LoginForm demoMode={false} />);
    expect(screen.getByRole('button', { name: 'Sign in' })).toBeDefined();
  });

  it('renders the SSO button', () => {
    render(<LoginForm demoMode={false} />);
    expect(screen.getByRole('button', { name: /sign in with microsoft/i })).toBeDefined();
  });

  it('hides the demo hint when demoMode is false', () => {
    render(<LoginForm demoMode={false} />);
    expect(screen.queryByText(/using the wachd demo/i)).toBeNull();
  });

  it('shows the demo hint when demoMode is true', () => {
    render(<LoginForm demoMode={true} />);
    expect(screen.getByText(/using the wachd demo/i)).toBeDefined();
    expect(screen.getByText(/check your email for login credentials/i)).toBeDefined();
  });
});
