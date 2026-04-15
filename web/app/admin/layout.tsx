'use client';

import { useEffect } from 'react';
import { useRouter, usePathname } from 'next/navigation';
import Link from 'next/link';
import { useSession } from '@/lib/session-context';
import { Navigation } from '@/components/navigation';

const adminNav = [
  { name: 'Overview', href: '/admin' },
  { name: 'Teams', href: '/admin/teams' },
  { name: 'Users', href: '/admin/users' },
  { name: 'Groups', href: '/admin/groups' },
  { name: 'SSO Providers', href: '/admin/sso' },
  { name: 'Password Policy', href: '/admin/password-policy' },
  { name: 'API Tokens', href: '/admin/tokens' },
];

export default function AdminLayout({ children }: { children: React.ReactNode }) {
  const { session, loading } = useSession();
  const router = useRouter();
  const pathname = usePathname();

  useEffect(() => {
    if (!loading && (!session || !session.isSuperAdmin)) {
      router.replace('/');
    }
  }, [session, loading, router]);

  if (loading || !session?.isSuperAdmin) {
    return null;
  }

  return (
    <>
      <Navigation />
      <div className="flex min-h-[calc(100vh-64px)]">
      {/* Sidebar */}
      <aside className="w-56 bg-gray-900 border-r border-gray-800 flex-shrink-0">
        <div className="p-4 border-b border-gray-800">
          <p className="text-xs font-semibold text-gray-400 uppercase tracking-wider">Admin Panel</p>
        </div>
        <nav className="p-2">
          {adminNav.map((item) => {
            const isActive = pathname === item.href;
            return (
              <Link
                key={item.name}
                href={item.href}
                className={`block px-3 py-2 rounded-md text-sm font-medium mb-1 transition-colors ${
                  isActive
                    ? 'bg-gray-800 text-white'
                    : 'text-gray-400 hover:bg-gray-800 hover:text-white'
                }`}
              >
                {item.name}
              </Link>
            );
          })}
        </nav>
      </aside>

      {/* Main content */}
      <main className="flex-1 p-8 bg-gray-50 min-h-full">{children}</main>
    </div>
    </>
  );
}
