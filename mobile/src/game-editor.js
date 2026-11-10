import {apiRequest} from './auth.js';
import {config} from './config.js';
import {gameURL,inputDate,inputTime,buildGame,responseError,gameFields} from './game-data.js';


function element(tag,text){const el=document.createElement(tag);if(text!==undefined)el.textContent=text;return el;}
async function request(url,method,data){const r=await apiRequest(url,method,data);if(r.status<200||r.status>=300)throw new Error(responseError(r));return r.data;}

export async function editGame(summary,onSaved){
 const dialog=element('dialog');dialog.className='game-editor';
 const title=element('h2',`Game ${summary.GameId} · ${summary.Association}`);dialog.append(title);
 const note=element('p','Loading game…');note.setAttribute('role','status');dialog.append(note);
 const close=element('button','Close');close.type='button';close.className='secondary';dialog.append(close);
 let working=false;
 const dismiss=()=>{if(working)return;dialog.close();dialog.remove();};close.onclick=dismiss;
 dialog.addEventListener('cancel',e=>{e.preventDefault();dismiss();});
 document.body.append(dialog);dialog.showModal();
 try{
  const url=gameURL(config.apiBase,summary);
  const snapshot=await request(url);
  if(!dialog.isConnected)return;
  const game=snapshot.game;
  const directory=await request(new URL('/api/mobile/officials',config.apiBase).href);
  if(!dialog.isConnected)return;
  const form=element('form');form.id='game-form';
  const inputs={};
  for(const [key,text,type='text'] of gameFields){
   const label=element('label',text),input=element('input');input.name=key;input.type=type;
   input.value=key==='Date'?inputDate(game[key]):key==='Time'?inputTime(game[key]):game[key]??'';
   input.required=['Date','Time','Sport','Level','Site','NumOfGames','TaxYear','GameFee','TravelPay','AssignorFee','Deductions'].includes(key);
   if(type==='number'){input.min=key==='NumOfGames'?'1':key==='TaxYear'?'2000':'0';input.step=['NumOfGames','TaxYear'].includes(key)?'1':'0.01';}
   if(key==='TaxYear')input.max='2100';
   if(key==='Date'){input.min='2000-01-01';input.max='2100-12-31';}
   inputs[key]=input;label.append(input);form.append(label);
  }
  const statusLabel=element('label','Status'),status=element('select');status.name='Status';
  for(const s of ['Pending','Completed','Cancelled',...(game.Status==='Paid'?['Paid']:[])])status.append(new Option(s,s));
  status.value=game.Status;statusLabel.append(status);form.append(statusLabel);
  const rolesBox=element('fieldset');rolesBox.className='official-fields';rolesBox.append(element('legend','Officials'));
  form.append(rolesBox);
  let assignments=(game.Officials||[]).map(o=>({...o})),roleGeneration=0,rolesLoading=false,rolesFailed=false;
  const renderRoles=()=>{
   rolesBox.replaceChildren(element('legend','Officials'));
   for(const assignment of assignments){
    const label=element('label',assignment.title),select=element('select');select.append(new Option('Unassigned',''));
    for(const official of directory||[])select.append(new Option(`${official.FirstName} ${official.LastName}`,String(official.Id)));
    if(assignment.officialId && ![...select.options].some(o=>o.value===assignment.officialId))select.append(new Option(assignment.name||'Saved official',assignment.officialId));
    // Preserve historical snapshots until a user changes the assignment.
    select.value=assignment.officialId||'';
    select.onchange=()=>{assignment.officialId=select.value;assignment.name=select.value?select.selectedOptions[0].textContent:'Unassigned';};
    label.append(select);rolesBox.append(label);
   }
  };
  const loadRoles=async()=>{
   const version=++roleGeneration;rolesLoading=true;rolesFailed=false;save.disabled=true;
   try{
    const rolesURL=new URL('/api/mobile/roles',config.apiBase);rolesURL.searchParams.set('sport',inputs.Sport.value);
    const result=await request(rolesURL.href);
    if(version!==roleGeneration||!dialog.isConnected)return;
    const saved=inputs.Sport.value===game.Sport?(game.Officials||[]):[];
    assignments=(result.roles||[]).filter(r=>r.active||saved.some(o=>o.roleId===r.roleId)).map(r=>saved.find(o=>o.roleId===r.roleId)||{roleId:r.roleId,title:r.title,displayOrder:r.displayOrder,officialId:'',name:'Unassigned'});
    for(const old of saved)if(!assignments.some(o=>o.roleId===old.roleId))assignments.push({...old});
    assignments=assignments.map(o=>({...o}));renderRoles();note.textContent='';
   }catch(error){if(version===roleGeneration){rolesFailed=true;note.textContent=error.message;}}
   finally{if(version===roleGeneration){rolesLoading=false;save.disabled=rolesFailed||game.Status==='Paid'||!snapshot.canWrite;}}
  };
  inputs.Sport.onchange=loadRoles;
  const notesLabel=element('label','Notes'),notes=element('textarea');notes.name='Notes';notes.value=game.Notes||'';notes.maxLength=snapshot.notesLimit;notes.rows=4;notesLabel.className='wide';notesLabel.append(notes);form.append(notesLabel);
  const warning=element('p','Game ID and association identify this assignment. Record payments on the website. Cancelling a game clears all fees.');warning.className='wide';form.append(warning);
  const save=element('button','Save changes');save.type='submit';form.append(save);
  const remove=element('button','Delete game');remove.type='button';remove.className='danger';form.append(remove);
  dialog.insertBefore(form,close);
  const lock=value=>{working=value;for(const el of form.elements)el.disabled=value;close.disabled=value;};
  const finish=async()=>{dialog.close();dialog.remove();await onSaved();};
  form.onsubmit=async e=>{
   e.preventDefault();if(working||rolesLoading||rolesFailed||!form.reportValidity())return;
   const values=Object.fromEntries(new FormData(form));
   if(values.Status==='Cancelled'&&game.Status!=='Cancelled'&&!confirm('Cancel this game and set all fees to zero?'))return;
   lock(true);note.textContent='Saving changes…';
   try{await request(url,'PUT',{game:buildGame(game,values,assignments),version:snapshot.version});await finish();}
   catch(error){note.textContent=error.message;lock(false);}
  };
  remove.onclick=async()=>{
   if(working||!confirm(`Permanently delete game ${game.GameId} for ${game.Association}?`))return;
   lock(true);note.textContent='Deleting game…';
   try{await request(url,'DELETE',{version:snapshot.version});await finish();}
   catch(error){note.textContent=error.message;lock(false);}
  };
  await loadRoles();
  if(game.Status==='Paid'||!snapshot.canWrite){for(const el of form.elements)el.disabled=true;note.textContent=game.Status==='Paid'?'Paid games cannot be edited or deleted.':'This account has read-only access.';}
 }catch(error){note.textContent=error.message;}
}
