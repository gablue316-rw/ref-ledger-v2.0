# Announcement management API

This document describes the Ref Ledger server API used by Ref Ledger Monitor
#19. Ref Ledger owns the authoritative announcement state and evaluates
eligibility using server time and the authenticated recipient identity.

## Authentication and transport

All management requests require the HTTP `Authorization` header using the
`Bearer` scheme and the exact secret configured as
`REF_LEDGER_LOG_API_TOKEN`. The configured secret is never returned by the API.
Requests with a short or missing configured token are rejected.

```http
Content-Type: application/json
```

Responses include a server-generated `X-Request-ID` for correlating a request
with Ref Ledger diagnostics. Authentication failures log the correlation ID,
bearer and transport classifications, and direct-TLS status. When an
authenticated request is rejected for transport, diagnostics describe the
number and safe values of `X-Forwarded-Proto` headers; unrecognized values
are redacted. A
missing database client returns `503` after authentication, and database
operation failures are logged separately. Never include bearer tokens, cookies,
session credentials, or request bodies when sharing diagnostic information.

`REF_LEDGER_LOG_API_TOKEN` must be configured on Ref Ledger and contain at
least 32 characters. Requests are accepted over HTTPS or loopback transport.
The announcement API does not require the logs API version header. An optional
`X-Ref-Ledger-Admin-Actor` header is recorded as `createdBy`; its value is
sanitized before storage. Do not send customer session credentials to these
endpoints.

## Management endpoints

Base path: `/api/internal/v1/admin/announcements`

| Method and path | Behavior | Success |
| --- | --- | --- |
| `GET /` | List uncleared announcements that have not expired, including pending announcements. | `200` with `{"announcements":[...]}` |
| `POST /` | Publish an announcement. | `201` with the created announcement |
| `GET /{id}` | Read one announcement. | `200` with the announcement |
| `PUT /{id}` | Replace an announcement's message, audience, and schedule. | `200` with the updated announcement |
| `POST /{id}/clear` | Clear an announcement immediately. | `200` with the cleared announcement |
| `DELETE /{id}` | Equivalent to clear. | `200` with the cleared announcement |
| `GET /sessions?username={username}` | Resolve a username and list its active sessions, for session-target selection. | `200` with account identity and sessions |

The session list response has this shape:

```json
{
  "tenantId": "account-id",
  "userId": "stable-user-id",
  "username": "person@example.test",
  "sessions": [
    {
      "sessionTargetId": "non-secret-target-id",
      "tenantId": "account-id",
      "userId": "stable-user-id",
      "username": "person@example.test",
      "platform": "web",
      "createdAt": "2026-11-26T14:00:00Z",
      "expiresAt": "2026-11-27T14:00:00Z"
    }
  ]
}
```

`sessionTargetId` is a separate, non-secret identifier for one sign-in. It is
not the web session cookie, mobile session token, or any other credential.
Treat it as a target selector, not as authentication.

## Publish and update request

`POST /` and `PUT /{id}` accept a JSON object with the following fields:

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `message` | string | Yes | Plain-text message, 1–4000 Unicode characters. Newlines and tabs are allowed; other control characters are rejected. |
| `audience` | string | Yes | `system`, `user`, or `session`. |
| `tenantId` | string | For `user`; optional for `session` | Stable tenant/account ID. User audiences require this and `userId`. A supplied session audience value must match the selected session. |
| `userId` | string | For `user`; optional for `session` | Stable user ID (the user record's 24-character hexadecimal ID). User audiences require this and `tenantId`. A supplied session audience value must match the selected session. |
| `username` | string | No | Optional verification for `user` (must match the selected tenant/user) or `session` (must match the selected session). It is not sufficient by itself to target a user. |
| `sessionTargetId` | string | For `session` | Non-secret target ID returned by the sessions endpoint. Ref Ledger verifies that the session is live and resolves to the supplied tenant/user, if supplied. |
| `startsAt` | string | No | Start time; omitted means now. |
| `endsAt` | string | No | Expiration time; omitted means no scheduled expiration. |
| `timezone` | string | If either date is supplied | IANA timezone used for local date/time input and date-only values. |
| `version` | integer | No | On update, optional optimistic concurrency check. The value must equal the current version or the update returns `409`. |

The JSON decoder rejects unknown fields. Time values can be RFC 3339, local
`YYYY-MM-DDTHH:MM[:SS]` in `timezone`, or `YYYY-MM-DD`. A date-only end date is
inclusive: it expires at midnight at the start of the following day in the
specified timezone. `endsAt` must be after `startsAt` and in the future.
Eligibility and expiration are evaluated using Ref Ledger server time.

### System-wide example

```http
POST /api/internal/v1/admin/announcements
```

```json
{
  "message": "Happy Thanksgiving",
  "audience": "system",
  "startsAt": "2026-11-26",
  "endsAt": "2026-11-26",
  "timezone": "America/New_York"
}
```

### Specific-user example

Use the stable `tenantId` and `userId` for the selected account/user pair. The
optional username is only a consistency check. Both IDs are required, and a
pair that does not resolve to the same user is rejected. This audience reaches
all eligible sessions for that exact user in that account, including sessions
created while the announcement is active.

```json
{
  "message": "Your account has a scheduled maintenance window.",
  "audience": "user",
  "tenantId": "tenant-7",
  "userId": "507f1f77bcf86cd799439011",
  "username": "person@example.test"
}
```

### Specific-session example

First call `GET /sessions?username=person%40example.test`, then use a
`sessionTargetId` from the returned live session list. A session target is
validated on publish and update; invalid, expired, revoked, or mismatched
targets are rejected. Only that authenticated sign-in is eligible. A new
sign-in does not inherit the announcement.

```json
{
  "message": "Please review this notice on your current device.",
  "audience": "session",
  "tenantId": "tenant-7",
  "userId": "507f1f77bcf86cd799439011",
  "username": "person@example.test",
  "sessionTargetId": "d0ef4bdc-9a65-4b67-8f25-d464861b29c7"
}
```

## Response schemas

Management reads and writes return an announcement object:

```json
{
  "id": "announcement-uuid",
  "message": "Happy Thanksgiving",
  "audience": "system",
  "startsAt": "2026-11-26T05:00:00Z",
  "endsAt": "2026-11-27T05:00:00Z",
  "timezone": "America/New_York",
  "cleared": false,
  "createdBy": "monitor",
  "createdAt": "2026-11-20T12:00:00Z",
  "updatedAt": "2026-11-20T12:00:00Z",
  "version": 1
}
```

Audience-specific fields are also present when applicable: `tenantId`,
`userId`, `username`, and `sessionTargetId`. Optional fields may be omitted.
`endsAt` and `clearedAt` are omitted when unset. Clear responses set
`cleared: true`, include `clearedAt`, and increment `version`.

## Errors

Errors are JSON objects of the form `{"error":"..."}` (authentication,
database-readiness, and unknown-route responses may be plain HTTP error text).

| Status | Meaning |
| --- | --- |
| `400 Bad Request` | Invalid JSON, unknown field, missing required field, invalid message/audience/timezone/date, or invalid date range. |
| `401 Unauthorized` | Missing, invalid, or unconfigured management bearer token, or disallowed transport. |
| `404 Not Found` | Unknown announcement, or tenant/user pair or optional username does not identify the same user. |
| `409 Conflict` | The supplied update `version` is stale, or concurrent modification prevented saving. Reload before retrying. |
| `422 Unprocessable Entity` | Session target is invalid, expired, revoked, or its tenant/user/username does not match. |
| `500 Internal Server Error` | Unexpected server-side failure. |
| `503 Service Unavailable` | The database client is not initialized. |

## Customer delivery contract

The signed-in web client reads `/api/announcements/current` and subscribes to
`/api/announcements/events` (SSE). The Android client long-polls
`/api/mobile/announcements/current?known=<key>`. These customer routes derive
recipient identity from authentication and return only the selected message:

```json
{
  "announcement": {
    "id": "announcement-uuid",
    "message": "Happy Thanksgiving",
    "version": 1,
    "startsAt": "2026-11-26T05:00:00Z",
    "endsAt": "2026-11-27T05:00:00Z"
  },
  "key": "announcement-uuid:1"
}
```

When nothing applies, `announcement` is `null` and `key` is an empty string.
Audience and recipient identity fields are never included in customer payloads.
