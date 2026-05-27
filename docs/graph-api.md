# Graph API

Incident graph endpoints are scoped per team under `/api/v1/teams/{teamId}`.

## Similar incidents

- `GET /incidents/{id}/similar`
- Role: viewer and above
- Response:

```json
{
  "data": [
    {
      "incident_id": "uuid",
      "title": "Payment timeout",
      "score": 0.84,
      "reason": "similar root cause; same service: checkout-api",
      "occurred_at": "2026-03-12T03:14:00Z",
      "resolution": "rolled back v2.3.1"
    }
  ],
  "error": null
}
```

## Incident subgraph

- `GET /incidents/{id}/graph`
- Role: viewer and above
- Returns the root node plus connected nodes/edges.

## Promote incident node

- `POST /incidents/{id}/promote`
- Role: admin only
- Promotes the incident node to `permanent` so it participates in similarity and traversal.

## Delete graph node

- `DELETE /graph/nodes/{nodeId}`
- Role: admin only
- Deletes the node and any connected edges inside the same `team_id` boundary.

## Graph config

- `GET /graph/config`
- Role: viewer and above
- `PUT /graph/config`
- Role: admin only

Payload:

```json
{
  "enabled": true,
  "min_similarity_score": 0.12
}
```

Validation:

- `teamId`, `incidentId`, and `nodeId` must be valid UUIDs
- `min_similarity_score` must be between `0.0` and `1.0`
- All graph endpoint success responses use the standard `{"data": ..., "error": null}` envelope
- `enabled=false` disables future graph write-back for the team, but previously written graph data remains readable
