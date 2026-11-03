// Public development settings. No client secret is needed.
const issuer = 'https://ref-ledger-dev.us.auth0.com/';

export const config = {
  apiBase: 'https://powell-valuable-bargains-arrives.trycloudflare.com',
  issuer,
  clientId: 'I3B7w10esvoTWsSwclbGngyHiSmD8wQ1',
  audience: 'https://api.ref-ledger.com/dev',

  authorizationBaseUrl: new URL('authorize', issuer).href,
  accessTokenEndpoint: new URL('oauth/token', issuer).href,
  revocationEndpoint: new URL('oauth/revoke', issuer).href,

  redirectUrl: 'com.refledger.mobile:/oauth2redirect',
  scope: 'openid offline_access dashboard:read'
};