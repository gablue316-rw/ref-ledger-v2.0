# Ref Ledger mobile dashboard v0.2 — Auth0 trial

Start with **AUTH0-TRIAL.md** in this directory for Auth0 setup, development
configuration, explicit account linking, build commands and trial acceptance
checks. This supersedes the provider-neutral introspection instructions in v0.1.

The delivery is an overlay: extract at the existing Ref Ledger repository root.
It contains new/changed backend files, go.mod/go.sum and the mobile project.
It is not a complete copy of the original repository. Website cookie login is
unchanged; mobile access is read-only through /api/mobile/dashboard.

For a sample-only browser preview:

```bash
cd mobile
npm ci
npm run dev
```

Choose Preview sample dashboard. Live sign-in is native-only; real tokens are
never stored using the secure-storage plugin's browser localStorage fallback.
Preview date/year controls do not filter sample data; live filters use Go.

Access tokens stay in memory. Refresh tokens use native secure storage and
refresh requests are serialized. The app's storage namespace includes issuer,
client and audience so test environments don't reuse one another's tokens.
Capacitor token/bridge logging and Android backups/cleartext traffic are disabled.

Validated here: signed JWT acceptance/rejection tests, web package tests/build,
six frontend tests and Vite production build. Native projects are generated and
synced; Android/iOS compilation, Auth0 signup, DNS setup and live provider/device
checks require your environment and have not been completed.
