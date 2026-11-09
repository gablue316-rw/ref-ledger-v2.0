// Production API with the existing Auth0 tenant. No client secret belongs in this app.
const issuer = 'https://ref-ledger-dev.us.auth0.com/';

export const config = {
  apiBase: 'https://www.ref-ledger.com',
  issuer,
  clientId: 'I3B7w10esvoTWsSwclbGngyHiSmD8wQ1',
  audience: 'https://api.ref-ledger.com/dev',

  authorizationBaseUrl: new URL('authorize', issuer).href,
  accessTokenEndpoint: new URL('oauth/token', issuer).href,
  revocationEndpoint: new URL('oauth/revoke', issuer).href,

  redirectUrl: 'com.refledger.mobile:/oauth2redirect',
  scope: 'openid email profile offline_access dashboard:read'
};