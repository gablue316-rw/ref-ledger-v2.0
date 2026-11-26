import {loadCatalog,bindGameFilters} from './catalog.js';
import {version as appVersion} from '../package.json';
document.getElementById('app-version').textContent=`Version ${appVersion}`;
import {createExpenses} from './expenses.js';
import {changeGameStatuses} from './game-status.js';
import {createGameActions} from './game-actions.js';
import {bulkGames} from './game-bulk.js';
import {editGame} from './game-editor.js';
import { Browser } from '@capacitor/browser';
import {login, logout, apiGet, registerAccount, sessionEndedEvent} from './auth.js';
import {config} from './config.js';
import {summarize,dashboardURL,assignmentEarnings} from './data.js';
import {createReports} from './reports.js';
import {createPayments} from './payments.js';
import {createSettings} from './settings.js';
import {createCatalogManagement} from './catalog-management.js';

const $=id=>document.getElementById(id),
 currency=n=>new Intl.NumberFormat('en-US',{
  style:'currency',
  currency:'USD'
 }).format(n);

let busy=false,authBusy=false,generation=0,canWrite=false,activePage='games';
const expenses=createExpenses();
let gameFilters=bindGameFilters($('filters'),{sports:[],associations:[],sites:[],levels:[]});
const reports=createReports($('reports-root'));
const payments=createPayments($('payments-root'));
const settings=createSettings($('settings-root'));
const catalogs=Object.fromEntries([['officials','official'],['associations','association'],['sites','site']].map(([page,type])=>[page,createCatalogManagement(type,$(`${page}-root`),async()=>{const current=generation,catalog=await loadCatalog();if(current===generation)gameFilters.updateCatalog(catalog);})]));
const closeMenu=()=>{$('menu-options').hidden=true;$('menu-toggle').setAttribute('aria-expanded','false');};
function showPage(page){
 activePage=page;closeMenu();
 const titles={games:'Games',expenses:'Expenses',reports:'Reports',payments:'Payments',officials:'Officials',associations:'Associations',sites:'Sites',settings:'Settings'};
 $('page-title').textContent=titles[page];
 $('dashboard').hidden=page!=='games';
 for(const p of Object.keys(titles)){
  const section=$(`${p}-page`);
  if(section)section.hidden=page!==p;
  const nav=$(`nav-${p}`);
  if(nav){if(p===page)nav.setAttribute('aria-current','page');else nav.removeAttribute('aria-current');}
 }
 message('');
 if(page==='expenses')expenses.load();
 if(page==='reports')reports.load();
 if(page==='payments')payments.load();
 if(page==='settings')settings.load();
 if(catalogs[page])catalogs[page].load();
}
$('menu-toggle').onclick=()=>{const open=$('menu-options').hidden;$('menu-options').hidden=!open;$('menu-toggle').setAttribute('aria-expanded',String(open));};
$('nav-games').onclick=()=>showPage('games');
$('nav-expenses').onclick=()=>showPage('expenses');
$('nav-reports').onclick=()=>showPage('reports');
$('nav-payments').onclick=()=>showPage('payments');
$('nav-settings').onclick=()=>showPage('settings');
for(const page of Object.keys(catalogs))$(`nav-${page}`).onclick=()=>showPage(page);
document.addEventListener('keydown',e=>{if(e.key==='Escape')closeMenu();});
document.addEventListener('click',e=>{if(!$('navigation').contains(e.target))closeMenu();});
const refreshGameCatalog=()=>{const current=generation;return catalog=>{if(current===generation)gameFilters.updateCatalog(catalog);};};
$('add-game').onclick=()=>editGame(null,async()=>{await load();message('Game added successfully. Adjust the filters if it is not shown.');},refreshGameCatalog());
const gameActions=createGameActions({container:$('games'),
 onEdit:g=>editGame(g,async text=>{await load();message(text);},refreshGameCatalog()),
 onDelete:g=>bulkGames([g],'DELETE',load),
 onBulk:(games,method)=>bulkGames(games,method,load),
 onStatus:(games,status)=>changeGameStatuses(games,status,load)
});

function updateControls() {
 for (const id of ['login','reauth','logout','refresh']) $(id).disabled=busy||authBusy;
 signupButton.disabled=busy||authBusy;
 gameActions.setDisabled(busy||authBusy||!canWrite);
 for(const id of ['menu-toggle','nav-games','nav-expenses','nav-reports','nav-payments','nav-officials','nav-associations','nav-sites','nav-settings','clear-game-filters'])$(id).disabled=busy||authBusy;
 $('add-game').disabled=busy||authBusy||!canWrite;
 reports.setCanWrite(canWrite);
 payments.setCanWrite(canWrite);
 settings.setCanWrite(canWrite);
 for(const catalog of Object.values(catalogs))catalog.setCanWrite(canWrite);
}

function clearDashboard() {
 ++generation;
 $('filters').reset();gameFilters.clearAll();gameFilters.setValues('status',['Pending']);
 expenses.clear();reports.clear();payments.clear();settings.clear();canWrite=false;activePage='games';$('page-title').textContent='Games';$('navigation').hidden=true;$('expenses-page').hidden=true;closeMenu();
 $('reports-page').hidden=true;$('payments-page').hidden=true;$('settings-page').hidden=true;
 for(const [page,catalog] of Object.entries(catalogs)){catalog.clear();$(`${page}-page`).hidden=true;}
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
   ['p',(()=>{const count=Number(g.NumOfGames??g.numOfGames??0);return Number.isFinite(count)&&count>=0?`${count} ${count===1?'game':'games'}`:'Game count unavailable';})()],
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
 $('dashboard').hidden=activePage!=='games';
 $('navigation').hidden=false;
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
  const formData=new FormData($('filters'));
  const filters=Object.fromEntries(formData.entries());
  for(const key of ['status','sport','association','assignor','site','level'])filters[key]=formData.getAll(key);

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
   r={status:200,data:[]};
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

  const access=await apiGet(new URL('/api/mobile/access',config.apiBase).href);
  if(current!==generation)return;
  canWrite=access.status===200&&access.data?.canWrite===true;
  const catalog=await loadCatalog();
  if(current!==generation)return;
  gameFilters.updateCatalog(catalog);
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
$('clear-game-filters').onclick=()=>{
 $('filters').reset();
 gameFilters.clearAll();
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

globalThis.addEventListener(sessionEndedEvent,()=>{
 clearDashboard();
 for(const dialog of document.querySelectorAll('dialog[open]')){
  dialog.close();
  if(dialog.parentElement===document.body)dialog.remove();
 }
 message('Your session ended. Please sign in again.');
 updateControls();
});

// Restore only inside a native build. Browser mode never reads or stores tokens.
import {Capacitor} from '@capacitor/core';
if(Capacitor.isNativePlatform())load();