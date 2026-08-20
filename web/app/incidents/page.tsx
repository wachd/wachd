import { IncidentsView } from './incidents-view';

export default function IncidentsPage() {
  const demoMode = process.env.DEMO_MODE === 'true';
  return <IncidentsView demoMode={demoMode} />;
}
