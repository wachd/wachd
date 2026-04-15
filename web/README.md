# Wachd Web Dashboard

Next.js 15 frontend for the Wachd alert intelligence platform.

## Overview

The dashboard provides:

- **Incidents** — live list of all open, acknowledged, and resolved alerts with severity badges
- **Incident detail** — root cause analysis, context (commits, logs, metrics), and one-click actions (acknowledge, resolve, snooze)
- **On-Call** — view the current on-call engineer and the weekly rotation schedule
- **Settings** — team configuration and notification channels

## Development

The frontend talks to the Wachd API server on `http://localhost:8080` by default.

### Start the full stack

```bash
# From the repo root
make dev
```

This starts PostgreSQL, Redis, the API server, the worker, and the Next.js dev server.

### Start only the frontend

```bash
make web
```

Opens at `http://localhost:3000`.

### Environment

```bash
# Optional — override the API base URL
NEXT_PUBLIC_API_URL=http://localhost:8080
```

## Project layout

```
web/
├── app/                 # Next.js App Router pages
│   ├── page.tsx         # Redirect to /incidents
│   ├── incidents/       # Incident list and detail views
│   ├── oncall/          # On-call schedule view
│   └── settings/        # Team settings
├── components/          # Shared UI components
│   └── navigation.tsx   # Top navigation bar
└── lib/
    ├── api.ts           # Typed API client
    └── types.ts         # Shared TypeScript types
```

## Build

```bash
npm run build
npm run start
```

## Tech stack

- [Next.js 15](https://nextjs.org) — App Router, React Server Components
- [Tailwind CSS](https://tailwindcss.com) — utility-first styling
- [TypeScript](https://www.typescriptlang.org) — end-to-end type safety
