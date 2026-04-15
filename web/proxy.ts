import { NextResponse } from 'next/server'
import type { NextRequest } from 'next/server'

const PUBLIC_PATHS = ['/login', '/auth/login', '/auth/callback']

export function proxy(request: NextRequest) {
  const { pathname } = request.nextUrl

  // Allow public paths through unconditionally
  if (PUBLIC_PATHS.some((p) => pathname === p || pathname.startsWith(p + '/'))) {
    return NextResponse.next()
  }

  // Static assets and Next.js internals are always public
  if (
    pathname.startsWith('/_next/') ||
    pathname.startsWith('/favicon') ||
    pathname.startsWith('/icons/') ||
    pathname.startsWith('/images/')
  ) {
    return NextResponse.next()
  }

  // Check for session cookie (optimistic check — no DB/Redis call here)
  const session = request.cookies.get('wachd_session')
  if (!session?.value) {
    const loginUrl = request.nextUrl.clone()
    loginUrl.pathname = '/login'
    return NextResponse.redirect(loginUrl)
  }

  return NextResponse.next()
}

export const config = {
  // Run on all page routes but skip API routes and static files
  matcher: ['/((?!_next/static|_next/image|favicon.ico|api/).*)'],
}
