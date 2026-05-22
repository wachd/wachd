import { NextResponse } from 'next/server'
import type { NextRequest } from 'next/server'

// Read at server startup from the runtime environment — works correctly in Docker
// because process.env is populated before the Next.js server module is loaded.
const BACKEND_URL = process.env.BACKEND_URL ?? 'http://localhost:8080'

const PUBLIC_PATHS = ['/login', '/auth/login', '/auth/local/login', '/auth/callback']

export function proxy(request: NextRequest) {
  const { pathname } = request.nextUrl

  // Proxy all /auth/* and /api/* requests to the Go backend.
  // Evaluated at runtime so BACKEND_URL is always the container-resolved value.
  if (pathname.startsWith('/auth/') || pathname.startsWith('/api/')) {
    const target = new URL(pathname, BACKEND_URL)
    target.search = request.nextUrl.search
    return NextResponse.rewrite(target)
  }

  // Allow public page paths through unconditionally
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
  // Run on all routes except static files; /auth/* and /api/* are handled above
  matcher: ['/((?!_next/static|_next/image|favicon.ico).*)'],
}
