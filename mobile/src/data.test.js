import {test} from 'node:test';import assert from 'node:assert/strict';import {summarize,dashboardURL} from './data.js';
test('summary uses net TotalEarnings and counts multiple games',()=>assert.deepEqual(summarize([{NumOfGames:2,TotalEarnings:'$125.50',GameFee:'$150.00'},{NumOfGames:1,TotalEarnings:'$75.00'}]),{assignments:2,games:3,earnings:200.5}));
test('dashboard query excludes client account identifiers',()=>{const u=new URL(dashboardURL('https://example.com',{status:'Pending',tenantId:'other',association:'A&B'}));assert.equal(u.searchParams.get('association'),'A&B');assert.equal(u.searchParams.has('tenantId'),false);});
test('reject insecure API origin',()=>assert.throws(()=>dashboardURL('http://example.com',{})));

import {normalizeTokens} from './data.js';
test('normalizes Android nested tokens and absolute millisecond expiry',()=>assert.deepEqual(normalizeTokens({access_token:'top',access_token_response:{access_token:'a',refresh_token:'r',expires_at:200000}},100000),{access:'a',refresh:'r',expires:200000}));
test('normalizes iOS refresh response',()=>assert.equal(normalizeTokens({access_token:'a',refresh_token:'r',expires_in:300},100000).expires,400000));
test('rejects missing rotation or expiry',()=>{assert.throws(()=>normalizeTokens({access_token:'a',expires_in:300}));assert.throws(()=>normalizeTokens({access_token:'a',refresh_token:'r'}));});

import {assignmentEarnings,authorizationParameters} from './data.js';
test('production dashboard sums net GameFee once per assignment',()=> {
 const games=[{NumOfGames:2,GameFee:'$155.25'},{NumOfGames:1,GameFee:'$80.10'}];
 assert.deepEqual(summarize(games),{assignments:2,games:3,earnings:235.35});
 assert.equal(assignmentEarnings(games[0]),155.25);
});
test('missing or empty TotalEarnings falls back to GameFee',()=> {
 for(const total of [undefined,null,'','  ']) assert.equal(assignmentEarnings({TotalEarnings:total,GameFee:'$120.50'}),120.50);
});
test('explicit zero net earnings is not replaced by a fee',()=> {
 for(const total of [0,'$0.00','0']) assert.equal(assignmentEarnings({TotalEarnings:total,GameFee:'$85.00'}),0);
});
test('money totals handle credits, thousands, cents and empty results',()=> {
 assert.equal(summarize([{GameFee:'$1,200.10'},{GameFee:'-$25.05'},{GameFee:'$0.10'},{GameFee:'$0.20'}]).earnings,1175.35);
 assert.deepEqual(summarize([]),{assignments:0,games:0,earnings:0});
});
test('fresh sign-in requests password connection and bypasses Auth0 SSO',()=> {
 assert.deepEqual(authorizationParameters({audience:'api'},false,true),{audience:'api',prompt:'login',max_age:'0',connection:'Username-Password-Authentication'});
 assert.equal(authorizationParameters({audience:'api',passwordConnection:'Custom'},false,true).connection,'Custom');
});
test('normal login keeps existing connection choice and signup behavior',()=> {
 assert.deepEqual(authorizationParameters({audience:'api'}),{audience:'api'});
 assert.deepEqual(authorizationParameters({audience:'api'},true),{audience:'api',screen_hint:'signup',prompt:'login'});
});
