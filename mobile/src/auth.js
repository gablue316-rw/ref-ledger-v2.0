import {Capacitor,CapacitorHttp} from '@capacitor/core';
import {GenericOAuth2} from '@capacitor-community/generic-oauth2';
import {SecureStoragePlugin as vault} from 'capacitor-secure-storage-plugin';
import {config as c} from './config.js';
import {normalizeTokens,authorizationParameters} from './data.js';
const key=`ref-ledger-refresh:${encodeURIComponent(c.issuer)}:${encodeURIComponent(c.clientId)}:${encodeURIComponent(c.audience)}`;
const sessionKey=`ref-ledger-session:${encodeURIComponent(c.issuer)}:${encodeURIComponent(c.clientId)}:${encodeURIComponent(c.audience)}`;
const sessionHeader='X-Ref-Ledger-Mobile-Session';
let access='',expires=0,refreshing;
async function sessionSecret(){try{return (await vault.get({key:sessionKey})).value;}catch{return '';}}
export async function clearLocalSession(){expireAccess();await vault.remove({key}).catch(()=>{});await vault.remove({key:sessionKey}).catch(()=>{});}
function sessionRevoked(response){
 if(response.status!==401)return false;
 const h=response.headers||{};
 return Object.keys(h).some(k=>k.toLowerCase()==='x-ref-ledger-session'&&h[k]==='invalid');
}
async function guarded(request){
 let response=await request();
 if(response.status===401&&!sessionRevoked(response)){expireAccess();response=await request();}
 if(sessionRevoked(response)){await clearLocalSession();response.sessionRevoked=true;}
 return response;
}
async function startSession(){
 const url=new URL('/api/mobile/session',c.apiBase).toString();
 const create=async()=>CapacitorHttp.post({url,headers:{Authorization:`Bearer ${await token()}`,Accept:'application/json'},connectTimeout:10000,readTimeout:25000});
 let response=await create();
 if(response.status===403){const setup=await registerAccount();if(setup.status===204||setup.status===200)response=await create();}
 // The rollout backend returns 404 while managed sessions are disabled.
 if(response.status===404)return;
 if(response.status!==200||!response.data?.sessionToken)throw new Error('Unable to start a Ref Ledger session.');
 await vault.set({key:sessionKey,value:response.data.sessionToken});
}
function native(){if(!Capacitor.isNativePlatform())throw new Error('Secure sign-in requires the Android or iPhone app.');}
function configured(){native();for(const k of ['issuer','authorizationBaseUrl','accessTokenEndpoint','revocationEndpoint']){if(new URL(c[k]).protocol!=='https:')throw new Error('Authentication configuration requires HTTPS.');}if(new URL(c.apiBase).protocol!=='https:'||!c.audience)throw new Error('Configure the development API and Auth0 audience.');if(!c.clientId)throw new Error('Configure your identity provider before signing in.');}
async function accept(result){
 const p=normalizeTokens(result);
 await vault.set({key,value:p.refresh});
 access=p.access;expires=p.expires;
 return access;
}
export async function login(signup=false,forceCredentials=false){
 configured();
 const platform={appId:c.clientId,responseType:'code',redirectUrl:c.redirectUrl,pkceEnabled:true,logsEnabled:false};
 const result=await accept(await GenericOAuth2.authenticate({appId:c.clientId,authorizationBaseUrl:c.authorizationBaseUrl,accessTokenEndpoint:c.accessTokenEndpoint,redirectUrl:c.redirectUrl,responseType:'code',scope:c.scope,additionalParameters:authorizationParameters(c,signup,forceCredentials),pkceEnabled:true,logsEnabled:false,android:platform, ios:platform}));
 try{await vault.remove({key:sessionKey}).catch(()=>{});await startSession();}catch(e){await clearLocalSession();throw e;}
 return result;
}
export async function token(){
 configured();if(access && Date.now()<expires-30000)return access;
 if(!refreshing)refreshing=(async()=>{
  const {value}=await vault.get({key});
  return accept(await GenericOAuth2.refreshToken({appId:c.clientId,accessTokenEndpoint:c.accessTokenEndpoint,refreshToken:value}));
 })().finally(()=>{refreshing=undefined;});
 return refreshing;
}
export function expireAccess(){access='';expires=0;}
export async function logout(){
 native();let failed=false;
 await refreshing?.catch(()=>{});
 try{
  const secret=await sessionSecret();
  if(secret){try{const ended=await CapacitorHttp.delete({url:new URL('/api/mobile/session',c.apiBase).toString(),headers:{Authorization:`Bearer ${await token()}`,[sessionHeader]:secret},connectTimeout:8000,readTimeout:8000});if(ended.status!==204&&ended.status!==401)failed=true;}catch{failed=true;}}
  const {value}=await vault.get({key});
  const data=new URLSearchParams({token:value,token_type_hint:'refresh_token',client_id:c.clientId}).toString();
  const response=await CapacitorHttp.post({url:c.revocationEndpoint,headers:{'Content-Type':'application/x-www-form-urlencoded'},data,connectTimeout:8000,readTimeout:8000});
  failed=response.status!==200;
 }catch{failed=true;}
 finally{await clearLocalSession();}
 return !failed;
}
export async function apiGet(url){
 const request=async()=>CapacitorHttp.get({url,headers:{Authorization:`Bearer ${await token()}`,[sessionHeader]:await sessionSecret(),Accept:'application/json'},connectTimeout:10000,readTimeout:15000});
 return guarded(request);
}

export async function registerAccount(){
 configured();
 const url=new URL('/api/mobile/register',c.apiBase).toString();
 const request=async()=>CapacitorHttp.post({
  url,
  headers:{Authorization:`Bearer ${await token()}`},
  connectTimeout:10000,
  readTimeout:25000
 });
 let response=await request();
 if(response.status===401){
  expireAccess();
  response=await request();
 }
 return response;
}

export async function apiRequest(url, method='GET', data, extraHeaders={}, readTimeout=25000) {
 configured();
 const target=new URL(url),base=new URL(c.apiBase);
 if(target.protocol!=='https:'||target.origin!==base.origin)throw new Error('Invalid API address.');
 const request=async()=>CapacitorHttp.request({url:target.href,method,
  headers:{Authorization:`Bearer ${await token()}`,[sessionHeader]:await sessionSecret(),Accept:'application/json','Content-Type':'application/json',...extraHeaders},
  ...(data===undefined?{}:{data}),connectTimeout:Math.min(10000,readTimeout),readTimeout});
 return guarded(request);
}
