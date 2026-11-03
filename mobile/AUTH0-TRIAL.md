# Auth0 development trial for Ref Ledger

## What is included

This v0.2 overlay replaces v0.1's introspection validator with Auth0's maintained
Go JWT middleware (v2.3.1). It validates RS256 signatures against the configured
issuer's HTTPS JWKS, plus issuer, audience, expiration, authorized native client
(`azp`), subject and `dashboard:read`. Keys are cached across requests. No
introspection API client or client secret is required. Website login is unchanged.
The app now sends the Auth0 API audience during authorization.

No Auth0 account, DNS entry, database update or deployment was made by this
package. Those require your account settings. This is source for a development
trial, not an installable or store-ready app.

## 1. Create the free Auth0 account and development application

Visit https://auth0.com/signup and create a development tenant. In its dashboard:

1. Applications > Applications > Create Application.
2. Name: `Ref Ledger Mobile Dev`; type: **Native**.
3. Use **None** for Token Endpoint Authentication Method.
4. Allowed Callback URLs: `com.refledger.mobile:/oauth2redirect` (exact match).
5. Enable Authorization Code and Refresh Token grant types; disable Implicit
   and Password grants for this app.
6. Enable Refresh Token Rotation. Set finite expiration/inactivity lifetimes
   (suggested trial values: seven days absolute and one day idle).
7. Copy the application's **Domain** and **Client ID**. Do not copy its secret.

The included callback scheme is already configured in both native projects.
Use verified HTTPS App Links/Universal Links as a production hardening step.

## 2. Register the development API

Applications > APIs > Create API:

- Name: `Ref Ledger API Dev`.
- Identifier: `https://api.ref-ledger.com/dev`.
- Signing algorithm: **RS256**. Keep the default Auth0 access-token profile
  (the validator checks its `azp` claim).
- Add permission `dashboard:read`.
- Enable **Allow Offline Access** so the app can receive refresh tokens.
- Set access-token lifetime to 300 seconds, including the browser-flow lifetime
  if a separate setting is shown.

The Identifier is a logical audience; you do not need to host a website there.
For this trial leave API RBAC disabled unless you also assign the permission to
your test user and configure scope issuance. The signed access token must contain
`dashboard:read` in its `scope`; the backend does not substitute `permissions`.
If prompted, authorize the application/API for first-party access and consent.

## 3. Enable hosted passkey login

Use Auth0 Universal Login with an Auth0-hosted database connection, enabled for
`Ref Ledger Mobile Dev`. Do not configure a custom connection to MongoDB.

Auth0's documented passkey setup requires a custom domain. Suggested development
name: `login-dev.ref-ledger.com`. Under Branding > Custom Domains, follow Auth0's
managed-certificate steps. Add only the DNS record Auth0 supplies; leave your
existing website records unchanged. Free custom domains require credit-card
verification. If using Cloudflare DNS, follow Auth0's instructions about proxying.

Under Authentication > Database > your connection > Authentication Methods,
enable Passkey. Enable user verification if available. Complete enrollment using
the Universal Login sign-up flow with an email address you control and verify it.
Test recovery as well as initial enrollment. Password fallback may remain enabled
for the trial, but that means sign-in is not passkey-only. Decide the recovery and
fallback policy before claiming phishing-resistant authentication for all logins.

Use the SAME custom domain (issuer) for authorization, tokens and backend
validation, e.g. `https://login-dev.ref-ledger.com/`. Creating/changing a domain
later affects passkey relying-party identity, so choose it before enrollment.

A password login on the default Auth0 domain can test token plumbing first, but
does not complete the requested passkey trial.

## 4. Configure the Go DEVELOPMENT server

Keep your existing development MongoDB environment settings. Set these additional
values (examples; use your actual issuer/client ID):

```bash
export MOBILE_OIDC_ISSUER='https://login-dev.ref-ledger.com/'
export MOBILE_API_AUDIENCE='https://api.ref-ledger.com/dev'
export MOBILE_OIDC_CLIENT_ID='YOUR_NATIVE_APPLICATION_CLIENT_ID'
```

Remove the old MOBILE_OIDC_INTROSPECTION_URL, MOBILE_API_CLIENT_ID and
MOBILE_API_CLIENT_SECRET from the v0.1 setup; v0.2 does not use them. Restart the
server after changing settings. The validator is initialized once per process.

Overlay the ZIP at your repository root, then run:

```bash
go test ./internal/mobileauth ./internal/html
go build -o ref-ledger-v2.exe ./internal/html
```

Expose this development server at an HTTPS address reachable from your phone.
The address must resolve from the phone; localhost points to the phone itself.
Verify the server's existing MongoDB settings select refLedger_v2_dev or your
chosen test database, rather than production.

## 5. Explicitly link YOUR existing development account

After creating/verifying your Auth0 test user, open User Management > Users and
copy its exact User ID (`auth0|...`). Verify that it is your user; email alone is
not sufficient to authorize linking.

In mongosh, connected to your DEVELOPMENT database, create this unique index:

```javascript
db.users.createIndex(
 {"mobileIdentity.issuer":1,"mobileIdentity.subject":1},
 {unique:true,partialFilterExpression:{
  "mobileIdentity.issuer":{$type:"string"},
  "mobileIdentity.subject":{$type:"string"}
 }}
)
```

Find your existing Ref Ledger user by username and confirm its _id/account.
Replace the placeholders and update by that exact _id:

```javascript
db.users.updateOne(
 {_id:ObjectId("YOUR_EXISTING_DEVELOPMENT_USER_ID")},
 {$set:{mobileIdentity:{
  issuer:"https://login-dev.ref-ledger.com/",
  subject:"auth0|YOUR_VERIFIED_AUTH0_USER_ID"
 }}}
)
```

Confirm matchedCount is 1. The account ID and role remain in your existing user
record. This creates no new ledger account and copies no financial data to Auth0.
Removing mobileIdentity blocks subsequent mobile API requests for that user.

## 6. Configure and run Android first

In mobile/src/config.js fill:

- issuer: exact HTTPS issuer ending in `/`.
- clientId: Native application's public Client ID.
- audience: the API Identifier from step 2.
- apiBase: HTTPS URL of YOUR development Go server.

```bash
cd mobile
npm ci
npm test
npm run build
npx cap sync
npx cap open android
```

Android Studio requires its SDK and supported JDK. Run on an emulator/phone,
choose Sign in, enroll/use your passkey, and check the dashboard matches your
existing development data. Browser preview remains sample-only; it will never
store real tokens in localStorage.

For iPhone, use a Mac with Xcode and CocoaPods, run npm ci/build and
`npx cap sync ios`, then `npx cap open ios`; configure your signing team.
Generated projects are included, but native compilation was not run here.

## 7. Test before any production rollout

- Dashboard shows your existing account's Pending games and applies filters.
- Restarting the app restores the session using the rotated refresh token.
- Access-token expiry obtains a new token without repeated login.
- Logout clears local tokens and successfully revokes the refresh token.
- Another unlinked Auth0 user receives 403 and no ledger records.
- Removing your mobileIdentity blocks further API requests.
- Original website cookie login still works.
- Passkey enrollment/recovery, cancelled login and network failure work on both
  actual phone platforms.

JWT validation is local: revoking a refresh token does not immediately invalidate
an already-issued access token. It can remain usable until expiry (five minutes
with the proposed trial settings). Account-link removal blocks requests through
this app immediately after the database update. Provider browser SSO logout is
not included. Uninstall may leave iOS Keychain data; establish a first-install
reset policy before production.

## Share only these public settings for the next configuration step

Auth0 issuer/domain, Native Client ID, API Identifier, and development API URL.
Do not send passwords, client secrets, refresh tokens or MongoDB credentials.
The Auth0 User ID is needed for the explicit local linkage; keep that linkage
operation under your own account control.

## Sources

https://developer.auth0.com/resources/guides/api/standard-library/basic-authorization
https://auth0.com/docs/secure/tokens/refresh-tokens/configure-refresh-token-rotation
https://developer.auth0.com/resources/labs/authentication/passkeys
https://support.auth0.com/center/s/article/Using-Custom-Domains-with-Free-Tenants
