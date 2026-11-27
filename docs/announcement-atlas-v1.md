# Monitor announcement commands, v1

This is the Atlas/MongoDB command contract, separate from the existing HTTPS
announcement API; that API and its authentication are unchanged. Ref Ledger
remains authoritative. The command collection is `refLedgerAnnouncementCommands`
in the same database as `announcements`, `users`, and `sessions`.

## Trust and configuration

The worker is off by default. Set
`REF_LEDGER_ANNOUNCEMENT_COMMANDS_ENABLED=true`, `APP_ENV` to the deployment
environment (default `development`), and
`REF_LEDGER_ANNOUNCEMENT_COMMAND_ACTORS` to a nonempty comma-separated list of
exact actor IDs. Each ID must match `[A-Za-z0-9._@:-]{1,128}`. Invalid
configuration disables the worker. Actor and environment fields are
allowlist/routing checks, **not authentication**: a writer can forge them.
Use a private network, TLS and distinct Atlas principals. Grant the Monitor
producer only insert and read on the command collection, not update/delete,
and deny it direct access to users, sessions, announcements and other
collections. Grant the Ref Ledger runtime the existing access to these
collections plus find/update on commands. Restrict command read access because
results contain account identifiers and announcement messages. Do not expose
the command collection to customer clients.

The Helm deployment passes both worker variables from the `env` values in
the repository's `values.yaml`; they default to `"false"` and `""`.
For an approved rollout, set `env.REF_LEDGER_ANNOUNCEMENT_COMMANDS_ENABLED`
to `"true"` and `env.REF_LEDGER_ANNOUNCEMENT_COMMAND_ACTORS` to the authorized
comma-separated actor IDs, with `env.APP_ENV` set to the intended environment.

Monitor must authenticate and authorize its operators, protect credentials,
restrict message content, and never put passwords, tokens, cookies, session
secrets or unrelated fields in commands. Unknown fields are rejected, but
inserted commands remain in Atlas, including any accidental secrets. Results
contain only explicitly selected fields; they are not a secret detector.
Set a retention policy for completed commands appropriate to the sensitivity
of messages and results; do not TTL-delete pending or processing commands.
Never reuse an ID for a different request.

## Envelope and operations

Insert one BSON document with `_id` and `requestId` equal to the same canonical
lowercase UUID string, `version: 1`, `state: "pending"`, `type`, `actor`,
`workerEnvironment` and `environment` (both exactly `APP_ENV`), and BSON dates
`createdAt` and `expiresAt`. The expiry must be in the future, after creation,
at most 15 minutes after creation; creation may be at most 30 seconds in the
future. The worker uses its own clock. Each operation below permits only the
listed additional fields; unknown/duplicate fields, wrong BSON types and
documents over 64 KiB are rejected. `attempts`, `leaseId`, and `leaseUntil`
are worker-owned; producers must omit them.

| type | Additional fields | Result |
|---|---|---|
| `list` | optional `after` | `announcements`, `nextCursor`; pending and active, not cleared or expired |
| `detail` | `announcementId` UUID | announcement, including cleared/expired |
| `accounts` | optional exact `username`, optional `after` | `accounts` of `{tenantId,userId,username}`, `nextCursor` |
| `sessions` | `tenantId`, `userId`, optional `after` | `sessions`, `nextCursor` |
| `publish` | `request` | created announcement |
| `edit` | `announcementId`, `request` | updated announcement |
| `clear` | `announcementId` | cleared announcement (already cleared is a no-op) |

`nextCursor` is empty at the end; otherwise issue a **new command with a new
UUID** and `after` equal to that cursor, preserving the other filters. Pages
contain at most 100 records and are ordered by `_id` ascending; they are not a
snapshot across commands, so concurrent inserts, deletes and expiry can change
the traversal. Account and session cursors are ObjectID hex; announcement
cursors are UUID strings.

Account selection uses the returned `tenantId` and `userId` (Mongo ObjectID
hex), not username alone. Duplicate usernames across tenants are supported;
ambiguous duplicate usernames within one tenant cannot safely resolve
sessions and are rejected. Sessions return only `sessionTargetId` (a random,
non-secret selector), `tenantId`, `userId`, `username`, `platform`,
`createdAt`, and `expiresAt`; no cookie, mobile token/hash or session ID is
returned. Only live web or managed mobile sessions are listed; mobile also
requires creation within the managed-session duration (15 minutes). Deleted,
expired, old mobile and mismatched sessions cannot be targeted. Older sessions
without a target ID receive one transactionally.
Session-targeted publish/edit also performs a conditional write to the live
session inside the announcement transaction. A concurrent deletion or
revocation therefore conflicts and forces revalidation rather than allowing a
stale snapshot to authorize a target.

`request` has string fields `message`, `audience`, optional `tenantId`,
`userId`, `username`, `sessionTargetId`, `startsAt`, `endsAt`, `timezone`;
edit additionally requires integer `version` equal to the current announcement
version. The body is a complete replacement, not a patch. Message is plain
text, 1–4000 Unicode characters; system/user/session audiences follow
`announcements-api.md`. User and session audiences require both stable IDs;
session also requires the selected target ID, checked against the live
session and account. Optional username is a consistency check. Timezone is
required if either time is supplied. Times accept RFC3339, local
`YYYY-MM-DDTHH:MM[:SS]`, or date-only; date-only ends are inclusive in the
named IANA timezone. End must be after start and in the future. Omitted start
means now; omitted end means no scheduled expiration.

## Processing and rollout

The worker polls every five seconds, up to 25 commands per batch. A claim
increments `attempts` and uses a 30-second lease. On crash an expired lease
can be reclaimed; three failed claims terminate as `failed/retry_exhausted`.
Malformed pending worker metadata is rejected as `malformed`, as is malformed
metadata on a processing command whose date-valued lease has expired. A
processing command whose lease expiry is missing or not a BSON date cannot
safely be determined expired and requires operator investigation; the worker
does not touch it or let it block the pending batch.
Non-scalar states matching `pending` are rejected without executing them.
If such a state also contains `processing`, it is only rejected when a
date-valued lease is expired; otherwise it is skipped to protect a possible
active claim and requires investigation if it does not become eligible.
Each poll gives a command at most three seconds, including the driver's
transaction retries. Pending expired commands are rejected, not executed.
Effects and the completed result are committed in one snapshot/majority Mongo
transaction, requiring a replica set or sharded deployment. A callback can be
retried, so notifications are sent only after successful commit; other pods
pick up changes through the existing polling. Once `state` is `completed`,
`outcome` is `success`, `rejected`, or `failed`, with `reason`, `result`,
`completedAt`, and `attempts`. Rejections use stable codes including
`malformed`, `invalid_request_id`, `invalid_version`, `invalid_environment`,
`invalid_actor`, `expired_or_invalid_lifetime`, `invalid_type`,
`invalid_announcement_id`, `invalid_account`, `invalid_cursor`,
`version_required`, `invalid_request`, `not_found`, `invalid_session`, and
`conflict`. Failed commands have no successful effect; investigate before
submitting a new ID. Poll only completed results. The unique Mongo `_id`,
conditional lease and transactional result provide idempotency for duplicate
delivery, restarts and multiple workers; never reset completed commands.

Roll out disabled, provision least-privilege principals and replica-set
transactions, deploy compatible Ref Ledger and Monitor, then enable in a
single nonproduction environment with an explicit actor. Exercise discovery,
publish, stale edit, clear, expiry and restart before enabling production.
Monitor failed/rejected counts and worker safe-category logs; do not log
command bodies. Disable the flag to stop new polling; in-flight work may
finish or recover when re-enabled. Reconcile completed results before retrying.
Confirm time synchronization and align retention with privacy policy.

After merging, link Monitor's implementation/runbook to this document using
the immutable repository URL at the actual merged commit SHA, e.g.
`https://github.com/<owner>/<repo>/blob/<40-character-commit-sha>/docs/announcement-atlas-v1.md`.
Do not use a branch name or guess the SHA before the commit exists.
