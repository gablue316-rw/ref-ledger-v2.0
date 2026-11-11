import {changeGameStatuses} from './game-status.js';
import {createGameActions} from './game-actions.js';
import {bulkGames} from './game-bulk.js';
import {editGame} from './game-editor.js';
import { Browser } from '@capacitor/browser';
import {login, logout, apiGet, registerAccount} from './auth.js';
import {config} from './config.js';
import {summarize,dashboardURL,assignmentEarnings} from './data.js';

const $=id=>document.getElementById(id),
 currency=n=>new Intl.NumberFormat('en-US',{
  style:'currency',
  currency:'USD'
 }).format(n);

let busy=false,authBusy=false,generation=0;
const gameActions=createGameActions({container:$('games'),
 onEdit:g=>editGame(g,load),
 onDelete:g=>bulkGames([g],'DELETE',load),
 onBulk:(games,method)=>bulkGames(games,method,load),
 onStatus:(games,status)=>changeGameStatuses(games,status,load)
});

function updateControls() {
 for (const id of ['login','reauth','logout','refresh']) $(id).disabled=busy||authBusy;
 signupButton.disabled=busy||authBusy;
 gameActions.setDisabled(busy||authBusy);
}

function clearDashboard() {
 ++generation;
 gameActions.reset();
 $('games').replaceChildren();
 for (const id of ['assignments','gameCount','earnings']) $(id).textContent='—';
 $('dashboard').hidden=true;
 $('signin').hidden=false;
 $('logout').hidden=true;
}

function message(text){
 $('message').textContent=text;
}

function render(games){
 gameActions.reset(games);
 const totals=summarize(games);
 $('assignments').textContent=totals.assignments;
 $('gameCount').textContent=totals.games;
 $('earnings').textContent=currency(totals.earnings);
 $('games').replaceChildren();

 for(const g of games){
  const card=document.createElement('article');
  card.className='game';

  for(const [tag,text] of [
   ['small',`${g.Status} · ${g.Sport} · ${g.Level}`],
   ['h2',`${g.Date} · ${g.Time}`],
   ['p',`${g.Site || '—'} · Field: ${g.Field || '—'}`],
   ['p',`${g.Home||'—'} vs ${g.Visitor||'—'}`],
   ['p',`${g.Association} · Game ${g.GameId}`],
   ['strong',currency(assignmentEarnings(g))]
  ]){
   const el=document.createElement(tag);
   el.textContent=text??'';
   card.append(el);
  }

  for(const official of g.Officials||[]){
   const el=document.createElement('p');
   el.textContent=`${official.title||''}: ${official.name||'Unassigned'}`;
   card.append(el);
  }

  gameActions.decorate(card,g);
  $('games').append(card);
 }

 $('signin').hidden=true;
 $('dashboard').hidden=false;
 $('logout').hidden=false;
 message(games.length?'':'No games match these filters.');
}

async function load(){
 if(busy)return;
 busy=true;
 const current=++generation;
 message('Loading games…');
 updateControls();

 try{
  const filters=Object.fromEntries(new FormData($('filters')));

  if(filters.begindate && filters.enddate &&
     filters.begindate>filters.enddate){
   throw new Error('The through date must follow the from date.');
  }

    const url = dashboardURL(config.apiBase, filters);
  let r = await apiGet(url);

  if (current !== generation) return;

  if (r.status === 403) {
   const setup = await registerAccount();

   if (current !== generation) return;

   if (setup.status === 204) {
    r = await apiGet(url);
   } else if (setup.status === 403) {
    throw new Error('Verify your email in Auth0, then sign in again. If you already verified it, contact Ref Ledger support.');
   } else if (setup.status === 409 && typeof setup.data === 'string') {
    throw new Error(setup.data.trim());
   } else if (setup.status === 502) {
    throw new Error('Could not verify your Auth0 profile. Sign in again and retry.');
   } else if (setup.status === 503) {
    throw new Error('Account setup is temporarily unavailable. Please try again.');
   } else if (setup.status === 401) {
    throw new Error('Your session expired. Sign out and sign in again.');
   } else {
    throw new Error('Unable to set up your account. Please retry or contact Ref Ledger support.');
   }
  }
  
  console.log('Dashboard response:', {
    status: r.status,
    dataIsArray: Array.isArray(r.data),
    dataType: typeof r.data
  });

  if(current!==generation)return;

  if(r.status===404 && r.data?.message === 'No games were found matching your search criteria.'){
   render([]);
   return;
  }

  if(r.status===401){
   throw new Error('Your session expired. Sign out and sign in again.');
  }

  if(r.status===403){
   throw new Error('Your mobile identity has not been linked to an authorized Ref Ledger account.');
  }

  if(r.status!==200||!Array.isArray(r.data)){
   throw new Error('Unable to load games. Check your connection and retry.');
  }

  render(r.data);
 }catch(error){
  if(current===generation){
   gameActions.reset();
   $('games').replaceChildren();
   $('assignments').textContent='—';
   $('gameCount').textContent='—';
   $('earnings').textContent='—';
   message(error.message||'Unable to connect. Please retry.');
  }
 }finally{
  busy=false;
  updateControls();
 }
}

const signupButton = document.createElement('button');
signupButton.type = 'button';
signupButton.className = 'secondary';
signupButton.textContent = 'Create account';
$('login').after(signupButton);

async function openAuth(signup, forceCredentials=false) {
 if (busy || authBusy) return;
 authBusy=true;
 updateControls();
 message(signup ? 'Opening account signup…' : 'Opening secure sign-in…');

 try {
  if (forceCredentials) {
   clearDashboard();
   $('filters').reset();
   await logout();
  }
  await login(signup,forceCredentials);
  await load();
 } catch (error) {
  // Show only a plugin error identifier.
  const detail = `${error?.code ?? ''} ${error?.message ?? ''}`;
  const code = detail.match(/\bERR_[A-Z0-9_]+\b/)?.[0];
  message(`Sign-in failed: ${code || 'No plugin error code returned'}`);
 } finally {
  authBusy=false;
  updateControls();
 }
}

$('login').onclick = () => openAuth(false);
signupButton.onclick = async () => {
 signupButton.disabled = true;
 try {
  const url = new URL('/mobile/create-account', config.apiBase);
  if (url.protocol !== 'https:') throw new Error('HTTPS is required.');
  await Browser.open({ url: url.href });
 } catch {
  message('Could not open the account page. Please try again.');
 } finally {
  signupButton.disabled = false;
 }
};

$('reauth').onclick=()=>openAuth(false,true);

$('refresh').onclick=load;

$('filters').onsubmit=e=>{
 e.preventDefault();
 load();
};

$('logout').onclick=async()=>{
 if (busy || authBusy) return;
 authBusy=true;
 updateControls();
 clearDashboard();
 message('Signing out…');
 try {
  const revoked=await logout();
  message(revoked ? 'Signed out.' : 'Signed out on this device. Server revocation could not be confirmed.');
 } catch {
  message('Could not finish signing out. Please retry.');
 } finally {
  authBusy=false;
  updateControls();
 }
};

// Restore only inside a native build. Browser mode never reads or stores tokens.
import {Capacitor} from '@capacitor/core';
if(Capacitor.isNativePlatform())load();