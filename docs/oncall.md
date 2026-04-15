# On-Call Scheduling

Wachd's on-call engine manages who gets notified when an alert fires. Each team configures one schedule that determines the on-call rotation. Overrides let any team member with the right role cover specific time windows without editing the underlying rotation.

---

## Table of Contents

- [Concepts](#concepts)
- [Who can manage schedules](#who-can-manage-schedules)
- [Schedule types](#schedule-types)
  - [Automatic rotation (layered)](#automatic-rotation-layered)
  - [Manual day-by-day (weekly)](#manual-day-by-day-weekly)
- [Creating or updating a schedule](#creating-or-updating-a-schedule)
- [Reading the current schedule](#reading-the-current-schedule)
- [Who is on-call right now](#who-is-on-call-right-now)
- [Schedule timeline](#schedule-timeline)
- [Overrides](#overrides)
  - [Creating an override](#creating-an-override)
  - [Listing upcoming overrides](#listing-upcoming-overrides)
  - [Deleting an override](#deleting-an-override)
- [Override resolution](#override-resolution)
- [UI reference](#ui-reference)

---

## Concepts

| Term | Meaning |
|---|---|
| Schedule | The team's on-call configuration — one per team, stored as a JSONB document in the `schedules` table |
| Rotation | The ordered list of members that cycle through on-call duty |
| Layer | A named sub-rotation within the schedule. Each layer has its own member list and time restriction. The current UI configures one layer |
| Override | A one-off substitution: a specific person covers a specific time window, superseding whoever the rotation would pick |
| Final user | The resolved on-call person after applying any override — the person who actually gets paged |

---

## Who can manage schedules

| Action | `viewer` | `responder` | `admin` |
|---|---|---|---|
| Read the schedule | Yes | Yes | Yes |
| View the timeline | Yes | Yes | Yes |
| See who is on-call now | Yes | Yes | Yes |
| Create or delete an override | No | Yes | Yes |
| Create or update the schedule itself | No | No | Yes |

---

## Schedule types

A schedule stores its rotation logic as a `rotation_config` JSONB document. There are two supported formats.

### Automatic rotation (layered)

The `"layered"` type is the recommended format. Members cycle through in order on a fixed interval. The engine computes the current slot as `floor((now - rotation_start) / rotation_interval_hours)` modulo the number of members.

```json
{
  "type": "layered",
  "rotation_start": "2026-04-07T00:00:00Z",
  "rotation_interval_hours": 168,
  "layers": [
    {
      "id": "layer-1",
      "name": "On-Call",
      "layer_order": 1,
      "time_restriction": { "type": "always" },
      "members": ["<uuid-alice>", "<uuid-bob>", "<uuid-carol>"]
    }
  ]
}
```

**`rotation_interval_hours` reference:**

| Value | Rotation cadence |
|---|---|
| `24` | Daily |
| `168` | Weekly |
| `336` | Bi-weekly |
| `720` | Monthly (~30 days) |

**`time_restriction.type` values:**

| Value | When the layer is active |
|---|---|
| `"always"` | Every day, all hours |
| `"weekdays"` | Monday through Friday only |
| `"weekends"` | Saturday and Sunday only |

`rotation_start` is an RFC3339 timestamp that anchors the rotation — it marks the moment person #1 (first in `members`) began their first shift. It does not need to be in the future.

Multiple layers are supported. Assign each a unique `id` and `layer_order`. Each layer resolves its on-call user independently based on its own member list and time restriction.

### Manual day-by-day (weekly)

The `"weekly"` type assigns a specific user to each named day of the week. Days not listed have no coverage.

```json
{
  "type": "weekly",
  "rotation": [
    { "day": "monday",    "user_id": "<uuid>" },
    { "day": "tuesday",   "user_id": "<uuid>" },
    { "day": "wednesday", "user_id": "<uuid>" },
    { "day": "thursday",  "user_id": "<uuid>" },
    { "day": "friday",    "user_id": "<uuid>" }
  ]
}
```

Valid day strings: `"monday"`, `"tuesday"`, `"wednesday"`, `"thursday"`, `"friday"`, `"saturday"`, `"sunday"`.

This format is useful when each person always owns the same day. For rotating duty, use `"layered"` instead.

---

## Creating or updating a schedule

`PUT /api/v1/teams/{teamId}/schedule` creates the schedule if none exists, or replaces the current one. This is an upsert — there is always at most one schedule per team.

**Create a weekly rotation (three engineers, one week each):**

```bash
curl -H "Authorization: Bearer wachd_..." \
  -X PUT http://localhost:8080/api/v1/teams/<teamId>/schedule \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Primary On-Call",
    "rotation_config": {
      "type": "layered",
      "rotation_start": "2026-04-07T00:00:00Z",
      "rotation_interval_hours": 168,
      "layers": [
        {
          "id": "layer-1",
          "name": "On-Call",
          "layer_order": 1,
          "time_restriction": { "type": "always" },
          "members": ["<uuid-alice>", "<uuid-bob>", "<uuid-carol>"]
        }
      ]
    }
  }'
```

**Create a weekday-only rotation:**

```bash
curl -H "Authorization: Bearer wachd_..." \
  -X PUT http://localhost:8080/api/v1/teams/<teamId>/schedule \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Business Hours On-Call",
    "rotation_config": {
      "type": "layered",
      "rotation_start": "2026-04-07T00:00:00Z",
      "rotation_interval_hours": 168,
      "layers": [
        {
          "id": "layer-1",
          "name": "Weekday",
          "layer_order": 1,
          "time_restriction": { "type": "weekdays" },
          "members": ["<uuid-alice>", "<uuid-bob>"]
        }
      ]
    }
  }'
```

**Create a manual weekly schedule:**

```bash
curl -H "Authorization: Bearer wachd_..." \
  -X PUT http://localhost:8080/api/v1/teams/<teamId>/schedule \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Fixed Weekly",
    "rotation_config": {
      "type": "weekly",
      "rotation": [
        { "day": "monday",    "user_id": "<uuid-alice>" },
        { "day": "tuesday",   "user_id": "<uuid-alice>" },
        { "day": "wednesday", "user_id": "<uuid-bob>" },
        { "day": "thursday",  "user_id": "<uuid-bob>" },
        { "day": "friday",    "user_id": "<uuid-carol>" }
      ]
    }
  }'
```

A successful response returns the created or updated schedule object.

---

## Reading the current schedule

```bash
curl -H "Authorization: Bearer wachd_..." \
  http://localhost:8080/api/v1/teams/<teamId>/schedule
```

If no schedule has been configured, the response body is `null` and `configured` is `false`. Otherwise the full schedule document is returned including `rotation_config`, `name`, and timestamps.

---

## Who is on-call right now

```bash
curl -H "Authorization: Bearer wachd_..." \
  http://localhost:8080/api/v1/teams/<teamId>/oncall/now
```

The response reflects the resolved on-call user at the current server time, after applying any active override. Example response:

```json
{
  "user": {
    "id": "550e8400-e29b-41d4-a716-446655440001",
    "name": "Alice Smith",
    "email": "alice@example.com"
  },
  "day": "monday"
}
```

Returns `null` / `configured: false` if no schedule is configured, or an empty response if the rotation has no coverage at the current time.

---

## Schedule timeline

The timeline endpoint shows day-by-day on-call assignments for a range of dates. It is used by the UI's calendar grid and is also useful for auditing upcoming coverage.

```
GET /api/v1/teams/{teamId}/oncall/timeline?from=YYYY-MM-DD&days=N
```

- `from`: start date in `YYYY-MM-DD` format. Defaults to today if omitted.
- `days`: number of days to return. Maximum is 42. Defaults to 14 if omitted.

**Example — fetch the next two weeks:**

```bash
curl -H "Authorization: Bearer wachd_..." \
  "http://localhost:8080/api/v1/teams/<teamId>/oncall/timeline?from=2026-04-07&days=14"
```

**Response structure:**

```json
{
  "schedule_name": "Primary On-Call",
  "layer_names": ["On-Call"],
  "from": "2026-04-07",
  "days": 14,
  "entries": [
    {
      "date": "2026-04-07",
      "layers": [
        {
          "layer_name": "On-Call",
          "user_id": "550e8400-e29b-41d4-a716-446655440001",
          "user_name": "Alice Smith"
        }
      ],
      "override": null,
      "final_user_id": "550e8400-e29b-41d4-a716-446655440001",
      "final_user_name": "Alice Smith"
    },
    {
      "date": "2026-04-14",
      "layers": [
        {
          "layer_name": "On-Call",
          "user_id": "550e8400-e29b-41d4-a716-446655440002",
          "user_name": "Bob Jones"
        }
      ],
      "override": {
        "user_id": "550e8400-e29b-41d4-a716-446655440003",
        "user_name": "Carol Lee",
        "reason": "Vacation swap"
      },
      "final_user_id": "550e8400-e29b-41d4-a716-446655440003",
      "final_user_name": "Carol Lee"
    }
  ]
}
```

Each entry covers one calendar day. Override presence is checked at noon UTC of each day. When `override` is non-null, `final_user_id` and `final_user_name` reflect the override user, not the rotation user.

---

## Overrides

An override replaces the rotation winner for a specific time window. It does not change the underlying rotation — once the override window ends, the rotation resumes normally.

### Creating an override

`POST /api/v1/teams/{teamId}/schedule/overrides`

Required fields:

| Field | Type | Description |
|---|---|---|
| `user_id` | UUID | The team member who will cover the window |
| `start_at` | RFC3339 | When the override begins |
| `end_at` | RFC3339 | When the override ends (exclusive) |
| `reason` | string | Optional — a human note (vacation, swap, sick leave) |

```bash
curl -H "Authorization: Bearer wachd_..." \
  -X POST http://localhost:8080/api/v1/teams/<teamId>/schedule/overrides \
  -H "Content-Type: application/json" \
  -d '{
    "user_id": "<uuid-carol>",
    "start_at": "2026-04-10T18:00:00Z",
    "end_at":   "2026-04-13T09:00:00Z",
    "reason":   "Vacation"
  }'
```

A successful response returns the created override object including its `id`.

The `user_id` must be a member of the team. The override window can span multiple days. Overlapping overrides are permitted — when two overrides cover the same instant, behaviour is unspecified, so avoid creating overlapping windows.

### Listing upcoming overrides

```bash
curl -H "Authorization: Bearer wachd_..." \
  http://localhost:8080/api/v1/teams/<teamId>/schedule/overrides
```

Returns up to 100 overrides whose `end_at` is in the future, ordered by `start_at` ascending. Past overrides are not returned.

Example response:

```json
{
  "overrides": [
    {
      "id": "7c9e6679-7425-40de-944b-e07fc1f90ae7",
      "user_id": "<uuid-carol>",
      "user_name": "Carol Lee",
      "start_at": "2026-04-10T18:00:00Z",
      "end_at":   "2026-04-13T09:00:00Z",
      "reason":   "Vacation",
      "created_by": "<uuid-alice>",
      "created_at": "2026-04-09T14:22:00Z"
    }
  ]
}
```

### Deleting an override

```bash
curl -H "Authorization: Bearer wachd_..." \
  -X DELETE http://localhost:8080/api/v1/teams/<teamId>/schedule/overrides/<overrideId>
```

Returns `204 No Content` on success. Requires `admin` or `responder` role on the team.

---

## Override resolution

When an alert fires at time T, the on-call engine resolves the final user in the following order:

1. Load the team's active schedule.
2. Check for an override where `start_at <= T < end_at`. If one exists, use that user — resolution stops here.
3. If no override matches, resolve from the rotation config:
   - For `"layered"`: compute `floor((T - rotation_start) / rotation_interval_hours) mod len(members)` for each layer whose time restriction includes T.
   - For `"weekly"`: find the rotation entry whose `day` matches the day-of-week of T.
4. The resolved user receives the alert notification.

If the schedule has no coverage for time T (for example, a `"weekdays"` layer on a weekend with no weekend layer, or a `"weekly"` schedule with Saturday and Sunday unassigned), no on-call user is resolved and the alert is still delivered — routed to team admins as a fallback.

---

## UI reference

The on-call page is available at `/oncall` for your team. It requires at least `viewer` role.

**Timeline grid** — OpsGenie-style calendar showing the full rotation period. Consecutive days owned by the same person are merged into a single bar. The grid has one row per rotation layer, an override row (visible only when overrides exist in the displayed period), and a Final Schedule row that shows the resolved on-call after applying overrides. Use the 7-day / 14-day toggle and the Previous / Next / Today navigation buttons to browse the calendar.

**+ Override button** — Visible to users with `admin` role. Opens an inline form in the timeline header to create an override without leaving the page. Fill in the covering user, date range, and optional reason, then submit.

**Admin panel (below the timeline)** — Visible to team admins only. Contains two tabs:

- **Members** — Edit phone numbers for each team member. Phone numbers are used for SMS and voice call notifications when an alert fires.
- **Edit Schedule** — Switch between rotation type (`layered` or `weekly`), configure the rotation interval, set members and their order, and save the schedule. Changes take effect immediately for any new alerts.
