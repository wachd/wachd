import { LoginForm } from './login-form';

export default function LoginPage() {
  const demoMode = process.env.DEMO_MODE === 'true';
  return <LoginForm demoMode={demoMode} />;
}
