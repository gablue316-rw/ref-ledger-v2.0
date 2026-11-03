import {login,logout,apiGet} from './auth.js';
import {config} from './config.js';
import {summarize,dashboardURL} from './data.js';

const $=id=>document.getElementById(id),
 currency=n=>new Intl.NumberFormat('en-US',{
  style:'currency',
  currency:'USD'
 }).format(n);

let preview=false,busy=false,generation=0;

const samples=[{
 GameId:1042,
 Date:'11/6/2026 (Fri)',
 Time:'7:00 PM',
 Sport:'Basketball',
 Level:'Varsity',
 Site:'Sample High School',
 Association:'MCBOA',
 Status:'Pending',
 Home:'Home team',
 Visitor:'Visiting team',
 NumOfGames:1,
 TotalEarnings:'$85.00',
 Officials:[{title:'Referee',name:'Sample Official'}]
}];

function message(text){
 $('message').textContent=text;
}

function render(games){
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
   ['p',g.Site],
   ['p',`${g.Home||'—'} vs ${g.Visitor||'—'}`],
   ['p',`${g.Association} · Game ${g.GameId}`],
   ['strong',g.TotalEarnings||g.GameFee]
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

  $('games').append(card);
 }

 $('signin').hidden=true;
 $('dashboard').hidden=false;
 $('logout').hidden=false;
 $('sample').hidden=!preview;
 message(games.length?'':'No games match these filters.');
}

async function load(){
 if(busy)return;
 busy=true;
 const current=++generation;
 message('Loading games…');
 $('refresh').disabled=true;

 try{
  const filters=Object.fromEntries(new FormData($('filters')));

  if(filters.begindate && filters.enddate &&
     filters.begindate>filters.enddate){
   throw new Error('The through date must follow the from date.');
  }

  if(preview){
   render(samples
    .filter(g=>!filters.status||g.Status===filters.status)
    .filter(g=>!filters.sport||
     g.Sport.toLowerCase()===filters.sport.toLowerCase())
    .filter(g=>!filters.association||
     g.Association.toLowerCase()===filters.association.toLowerCase()));
   return;
  }

  const r=await apiGet(dashboardURL(config.apiBase,filters));
  
  console.log('Dashboard response:', {
    status: r.status,
    dataIsArray: Array.isArray(r.data),
    dataType: typeof r.data
  });

  if(current!==generation)return;

  if(r.status===404){
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
   $('games').replaceChildren();
   $('assignments').textContent='—';
   $('gameCount').textContent='—';
   $('earnings').textContent='—';
   message(error.message||'Unable to connect. Please retry.');
  }
 }finally{
  busy=false;
  $('refresh').disabled=false;
 }
}

$('login').onclick=async()=>{
 $('login').disabled=true;
 message('Opening secure sign-in…');

 try{
  await login();
  preview=false;
  await load();
 }catch(error){
  // Show only a plugin error identifier, never raw authentication details.
  const detail=`${error?.code??''} ${error?.message??''}`;
  const code=detail.match(/\bERR_[A-Z0-9_]+\b/)?.[0];
  message(`Sign-in failed: ${code||'No plugin error code returned'}`);
 }finally{
  $('login').disabled=false;
 }
};

$('preview').onclick=()=>{
 preview=true;
 load();
};

$('refresh').onclick=load;

$('filters').onsubmit=e=>{
 e.preventDefault();
 load();
};

$('logout').onclick=async()=>{
 ++generation;
 const sample=preview;
 preview=false;
 $('games').replaceChildren();
 $('dashboard').hidden=true;
 $('signin').hidden=false;
 $('logout').hidden=true;
 message('');

 if(!sample){
  const revoked=await logout();
  message(revoked
   ?'Signed out.'
   :'Signed out on this device. Server revocation could not be confirmed; use your identity-provider session controls if needed.');
 }
};

// Restore only inside a native build. Browser preview never reads or stores tokens.
import {Capacitor} from '@capacitor/core';
if(Capacitor.isNativePlatform())load();