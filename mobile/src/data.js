export function money(value) {
 const n=Number(String(value??'0').replace(/[$,\s]/g,''));
 return Number.isFinite(n)?n:0;
}
export function summarize(games) {
 return games.reduce((sum,g)=>({assignments:sum.assignments+1,games:sum.games+Number(g.NumOfGames||0),earnings:sum.earnings+money(g.TotalEarnings)}),{assignments:0,games:0,earnings:0});
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
