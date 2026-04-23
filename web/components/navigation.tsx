'use client';

import Link from 'next/link';
import { usePathname, useRouter } from 'next/navigation';
import { useSession } from '@/lib/session-context';
import { api } from '@/lib/api';

const navItems = [
  { name: 'Incidents', href: '/incidents' },
  { name: 'On-Call', href: '/oncall' },
  { name: 'Settings', href: '/settings' },
];

export function Navigation() {
  const pathname = usePathname();
  const router = useRouter();
  const { session } = useSession();

  const allNavItems = [
    ...navItems,
    ...(session?.isSuperAdmin ? [{ name: 'Admin', href: '/admin' }] : []),
  ];

  async function handleLogout() {
    try {
      await api.auth.logout();
    } catch {
      // ignore
    }
    router.push('/login');
  }

  return (
    <nav className="bg-gray-900 border-b border-gray-800">
      <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8">
        <div className="flex items-center justify-between h-16">
          <div className="flex items-center">
            <Link href="/" className="flex items-center">
              <span className="text-xl font-bold text-white">Wachd</span>
            </Link>
            <div className="ml-10 flex items-baseline space-x-4">
              {allNavItems.map((item) => {
                const isActive = pathname === item.href;
                return (
                  <Link
                    key={item.name}
                    href={item.href}
                    className={`px-3 py-2 rounded-md text-sm font-medium transition-colors ${
                      isActive
                        ? 'bg-gray-800 text-white'
                        : 'text-gray-300 hover:bg-gray-700 hover:text-white'
                    }`}
                  >
                    {item.name}
                  </Link>
                );
              })}
            </div>
          </div>

          {session && (
            <div className="flex items-center gap-3">
              <Link
                href="/profile"
                className="text-sm text-gray-300 hover:text-white hidden sm:block transition-colors"
                title="Notification rules"
              >
                {session.name}
              </Link>
              <button
                onClick={handleLogout}
                className="text-sm text-gray-400 hover:text-white transition-colors px-3 py-1.5 rounded-md hover:bg-gray-800"
              >
                Sign out
              </button>
            </div>
          )}
        </div>
      </div>
    </nav>
  );
}
