import {Capacitor,CapacitorHttp} from '@capacitor/core';
import {GenericOAuth2} from '@capacitor-community/generic-oauth2';
import {SecureStoragePlugin as vault} from 'capacitor-secure-storage-plugin';
import {config as c} from './config.js';
import {normalizeTokens} from './data.js';
const key=`ref-ledger-refresh:${encodeURIComponent(c.issuer)}:${encodeURIComponent(c.clientId)}:${encodeURIComponent(c.audience)}`;
let access='',expires=0,refreshing;
function native(){if(!Capacitor.isNativePlatform())throw new Error('Secure sign-in requires the Android or iPhone app.');}
function configured(){native();for(const k of ['issuer','authorizationBaseUrl','accessTokenEndpoint','revocationEndpoint']){if(new URL(c[k]).protocol!=='https:')throw new Error('Authentication configuration requires HTTPS.');}if(new URL(c.apiBase).protocol!=='https:'||!c.audience)throw new Error('Configure the development API and Auth0 audience.');if(!c.clientId)throw new Error('Configure your identity provider before signing in.');}
async function accept(result){
 const p=normalizeTokens(result);
 await vault.set({key,value:p.refresh});
 access=p.access;expires=p.expires;
 return access;
}
export async function login(signup=false){
 configured();
 const platform={appId:c.clientId,responseType:'code',redirectUrl:c.redirectUrl,pkceEnabled:true,logsEnabled:false};
 return accept(await GenericOAuth2.authenticate({appId:c.clientId,authorizationBaseUrl:c.authorizationBaseUrl,accessTokenEndpoint:c.accessTokenEndpoint,redirectUrl:c.redirectUrl,responseType:'code',scope:c.scope,additionalParameters:{audience:c.audience,...(signup?{screen_hint:'signup',prompt:'login'}:{})},pkceEnabled:true,logsEnabled:false,android:platform,ios:platform}));
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
  const {value}=await vault.get({key});
  const data=new URLSearchParams({token:value,token_type_hint:'refresh_token',client_id:c.clientId}).toString();
  const response=await CapacitorHttp.post({url:c.revocationEndpoint,headers:{'Content-Type':'application/x-www-form-urlencoded'},data,connectTimeout:8000,readTimeout:8000});
  failed=response.status!==200;
 }catch{failed=true;}
 finally{expireAccess();await vault.remove({key}).catch(()=>{});}
 return !failed;
}
export async function apiGet(url){
 const request=async()=>CapacitorHttp.get({url,headers:{Authorization:`Bearer ${await token()}`,Accept:'application/json'},connectTimeout:10000,readTimeout:15000});
 let response=await request();if(response.status===401){expireAccess();response=await request();}
 return response;
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
