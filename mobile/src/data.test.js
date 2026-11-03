import {test} from 'node:test';import assert from 'node:assert/strict';import {summarize,dashboardURL} from './data.js';
test('summary uses net TotalEarnings and counts multiple games',()=>assert.deepEqual(summarize([{NumOfGames:2,TotalEarnings:'$125.50',GameFee:'$150.00'},{NumOfGames:1,TotalEarnings:'$75.00'}]),{assignments:2,games:3,earnings:200.5}));
test('dashboard query excludes client account identifiers',()=>{const u=new URL(dashboardURL('https://example.com',{status:'Pending',tenantId:'other',association:'A&B'}));assert.equal(u.searchParams.get('association'),'A&B');assert.equal(u.searchParams.has('tenantId'),false);});
test('reject insecure API origin',()=>assert.throws(()=>dashboardURL('http://example.com',{})));

import {normalizeTokens} from './data.js';
test('normalizes Android nested tokens and absolute millisecond expiry',()=>assert.deepEqual(normalizeTokens({access_token:'top',access_token_response:{access_token:'a',refresh_token:'r',expires_at:200000}},100000),{access:'a',refresh:'r',expires:200000}));
test('normalizes iOS refresh response',()=>assert.equal(normalizeTokens({access_token:'a',refresh_token:'r',expires_in:300},100000).expires,400000));
test('rejects missing rotation or expiry',()=>{assert.throws(()=>normalizeTokens({access_token:'a',expires_in:300}));assert.throws(()=>normalizeTokens({access_token:'a',refresh_token:'r'}));});
