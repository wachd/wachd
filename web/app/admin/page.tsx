'use client';

import Link from 'next/link';

const sections = [
  {
    title: 'Users',
    description: 'Manage local user accounts, reset passwords, activate/deactivate users.',
    href: '/admin/users',
    icon: '👤',
  },
  {
    title: 'Groups',
    description: 'Create groups, assign members, and grant teams access.',
    href: '/admin/groups',
    icon: '👥',
  },
  {
    title: 'SSO Providers',
    description: 'Configure OIDC providers (Entra, Google, Okta). Secrets stored encrypted.',
    href: '/admin/sso',
    icon: '🔑',
  },
  {
    title: 'Password Policy',
    description: 'Set complexity requirements, lockout thresholds, and duration.',
    href: '/admin/password-policy',
    icon: '🔒',
  },
];

export default function AdminDashboard() {
  return (
    <div className="max-w-4xl">
      <h1 className="text-2xl font-bold text-gray-900 mb-2">Admin Panel</h1>
      <p className="text-gray-500 mb-8">Superadmin access — manage users, groups, SSO, and security policy.</p>

      <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
        {sections.map((s) => (
          <Link
            key={s.href}
            href={s.href}
            className="block bg-white border border-gray-200 rounded-lg p-6 hover:border-blue-300 hover:shadow-sm transition-all"
          >
            <div className="text-3xl mb-3">{s.icon}</div>
            <h2 className="text-base font-semibold text-gray-900 mb-1">{s.title}</h2>
            <p className="text-sm text-gray-500">{s.description}</p>
          </Link>
        ))}
      </div>
    </div>
  );
}
