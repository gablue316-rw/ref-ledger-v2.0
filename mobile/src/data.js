export function money(value) {
 const n=Number(String(value??'0').replace(/[$,\s]/g,''));
 return Number.isFinite(n)?n:0;
}
// Dashboard GameFee is already the server-calculated net assignment amount.
// Prefer TotalEarnings when supplied, including an explicit zero.
export function assignmentEarnings(game) {
 const total = game.TotalEarnings;
 return money(total != null && String(total).trim() !== '' ? total : game.GameFee);
}
export function summarize(games) {
 const totals = games.reduce((sum, game) => ({
  assignments: sum.assignments + 1,
  games: sum.games + Number(game.NumOfGames || 0),
  cents: sum.cents + Math.round(assignmentEarnings(game) * 100)
 }), {assignments: 0, games: 0, cents: 0});
 return {assignments: totals.assignments, games: totals.games, earnings: totals.cents / 100};
}

export function authorizationParameters(config, signup=false, forceCredentials=false) {
 return {
  audience: config.audience,
  ...(signup ? {screen_hint: 'signup', prompt: 'login'} : {}),
  ...(forceCredentials ? {
   prompt: 'login', max_age: '0',
   connection: config.passwordConnection || 'Username-Password-Authentication'
  } : {})
 };
}
export function dashboardURL(base,filters) {
 const url=new URL('/api/mobile/dashboard',base);
 if(url.protocol!=='https:')throw new Error('HTTPS is required.');
 for(const key of ['status','sport','association','begindate','enddate','taxYear']) {
  if(filters[key])url.searchParams.set(key,filters[key]);
 }
 return url.href;
}

export function normalizeTokens(result, now=Date.now()) {
 const p=result.access_token_response??result;
 const expiry=p.expires_at!=null?Number(p.expires_at):now+Number(p.expires_in)*1000;
 if(typeof p.access_token!=='string'||!p.access_token||typeof p.refresh_token!=='string'||!p.refresh_token||!Number.isFinite(expiry)||expiry<=now)throw new Error('Provider must return expiring access tokens and rotating refresh tokens.');
 return {access:p.access_token,refresh:p.refresh_token,expires:expiry};
}
