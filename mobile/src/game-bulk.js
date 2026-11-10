import {apiRequest} from './auth.js';
import {config} from './config.js';
import {gameURL,gameFields,responseError} from './game-data.js';
import {applyBulkChanges,runBulkWrites} from './game-bulk-data.js';

const el=(tag,text)=>{const node=document.createElement(tag);if(text!==undefined)node.textContent=text;return node;};
async function get(url){const r=await apiRequest(url);if(r.status!==200)throw new Error(responseError(r));return r.data;}
export async function bulkGames(games,mode,onChanged){
 if(!games.length)return;
 const deleting=mode==='DELETE';
 const dialog=el('dialog');dialog.className='game-editor';dialog.setAttribute('aria-label',deleting?'Delete selected games':'Update selected games');
 dialog.append(el('h2',`${deleting?'Delete':'Update'} ${games.length} selected game${games.length===1?'':'s'}`));
 const note=el('p','Checking selected assignments…');note.setAttribute('role','status');dialog.append(note);
 const targets=el('details'),summary=el('summary','Selected assignments'),list=el('ul');targets.append(summary,list);
 for(const g of games)list.append(el('li',`${g.Association} · Game ${g.GameId} · ${g.Date} · ${g.Site || ''} · Field ${g.Field || '—'}`));dialog.append(targets);
 const close=el('button','Close');close.type='button';close.className='secondary';dialog.append(close);
 let working=false;
 const dismiss=()=>{if(!working){dialog.close();dialog.remove();}};close.onclick=dismiss;dialog.addEventListener('cancel',e=>{e.preventDefault();dismiss();});
 document.body.append(dialog);dialog.showModal();
 try{
  const snapshots=[];
  // Read in small groups so a large selection does not flood the API.
  for(let i=0;i<games.length;i+=4){
   const group=await Promise.all(games.slice(i,i+4).map(g=>get(gameURL(config.apiBase,g))));
   if(!dialog.isConnected)return;
   snapshots.push(...group);
   note.textContent=`Checked ${snapshots.length} of ${games.length} assignments…`;
  }
  if(snapshots.some(s=>!s.canWrite))throw new Error('This account has read-only access. No games changed.');
  if(snapshots.some(s=>s.game.Status==='Paid'))throw new Error('A selected game is now Paid. Close, refresh, and select unpaid games. No games changed.');
  const form=el('form');form.className='bulk-form';
  const changes=new Map(),officialChanges=new Map();
  const intro=el('p',deleting?'Deletion is permanent. Review the selected assignments before continuing.':'Check each field you want to apply to all selected games. Unchecked fields keep their individual values.');intro.className='wide';form.append(intro);
  if(!deleting){
   const specs=[['Status','Status','select'],...gameFields,['Notes','Notes','textarea']];
   for(const [key,title,type='text'] of specs){
    const row=el('div');row.className='bulk-field';
    const label=el('label'),check=el('input');check.type='checkbox';check.className='apply-field';check.setAttribute('aria-label',`Apply ${title} to selected games`);label.append(check,document.createTextNode(`Change ${title}`));row.append(label);
    const input=el(type==='select'?'select':type==='textarea'?'textarea':'input');input.name=key;input.setAttribute('aria-label',title);input.disabled=true;
    if(type==='select')for(const s of ['Pending','Completed','Cancelled'])input.append(new Option(s,s));
    else if(type!=='textarea'){input.type=type;}
    if(type==='number'){input.min=key==='TaxYear'?'2000':key==='NumOfGames'?'1':'0';input.step=['TaxYear','NumOfGames'].includes(key)?'1':'0.01';if(key==='TaxYear')input.max='2100';}
    if(type==='date'){input.min='2000-01-01';input.max='2100-12-31';}
    if(type==='textarea'){input.rows=3;input.maxLength=Math.min(...snapshots.map(s=>s.notesLimit));}
    check.onchange=()=>{input.disabled=!check.checked;input.required=check.checked&&!['Notes','Field','Home','Visitor','Assignor'].includes(key);if(check.checked)changes.set(key,input);else changes.delete(key);};
    row.append(input);form.append(row);
   }
   const sports=new Set(snapshots.map(s=>s.game.Sport));
   if(sports.size===1){
    const sport=[...sports][0],rolesURL=new URL('/api/mobile/roles',config.apiBase);rolesURL.searchParams.set('sport',sport);
    const [directory,definitions]=await Promise.all([get(new URL('/api/mobile/officials',config.apiBase).href),get(rolesURL.href)]);
    if(!dialog.isConnected)return;
    const officials=el('fieldset');officials.className='official-fields';officials.append(el('legend',`Officials · ${sport}`));
    for(const role of (definitions.roles||[]).filter(r=>r.active)){
     const label=el('label'),check=el('input');check.type='checkbox';check.className='apply-field';check.setAttribute('aria-label',`Apply official for ${role.title}`);label.append(check,document.createTextNode(`Change ${role.title}`));
     const select=el('select');select.disabled=true;select.setAttribute('aria-label',`Official for ${role.title}`);select.append(new Option('Unassigned',''));
     for(const official of directory||[])select.append(new Option(`${official.FirstName} ${official.LastName}`,String(official.Id)));
     check.onchange=()=>{select.disabled=!check.checked;if(check.checked)officialChanges.set(role.roleId,{role,select});else officialChanges.delete(role.roleId);};label.append(select);officials.append(label);
    }
    form.append(officials);
   }else{const p=el('p','Select games from one sport to update officials together.');p.className='wide';form.append(p);}
   const warning=el('p','Cancelling clears all fees. Changing sport resets official assignments; assign the new sport’s officials afterward.');warning.className='wide';form.append(warning);
  }
  const action=el('button',deleting?'Delete all selected':'Update all selected');action.type='submit';if(deleting)action.className='danger';form.append(action);dialog.insertBefore(form,close);
  note.textContent=`${games.length} assignments ready.`;
  form.onsubmit=async e=>{
   e.preventDefault();if(working||!form.reportValidity())return;
   const values=Object.fromEntries([...changes].map(([key,input])=>[key,input.value]));
   if(!deleting&&!changes.size&&!officialChanges.size){note.textContent='Check at least one field or official to update.';return;}
   if(values.Sport!==undefined&&officialChanges.size){note.textContent='Change sport and officials in separate updates.';return;}
   const text=deleting?`Permanently delete all ${games.length} selected games?`:`Apply these changes to all ${games.length} selected games?${values.Status==='Cancelled'?' All fees will be set to zero.':''}`;
   if(!confirm(text))return;
   working=true;close.disabled=true;for(const input of form.elements)input.disabled=true;
   const selectedOfficials=Object.fromEntries([...officialChanges].map(([key,{role,select}])=>[key,{title:role.title,displayOrder:role.displayOrder,officialId:select.value,name:select.value?select.selectedOptions[0].textContent:'Unassigned'}]));
   const results=await runBulkWrites(snapshots,deleting?'DELETE':'PUT',original=>{
    const updated=applyBulkChanges(original,values,selectedOfficials);
    if(values.Sport!==undefined&&values.Sport!==original.Sport)updated.Officials=[];
    return updated;
   },(game,method,data)=>apiRequest(gameURL(config.apiBase,game),method,data),(count,total)=>{note.textContent=`${deleting?'Deleting':'Updating'} ${count} of ${total}…`;});
   const succeeded=results.filter(r=>r.ok).length,failed=results.length-succeeded;
   note.textContent=`${succeeded} ${deleting?'deleted':'updated'}; ${failed} ${deleting?'not confirmed deleted':'not updated'}.`;
   const report=el('ul');report.className='bulk-results';
   for(const result of results)report.append(el('li',`${result.game.Association} · Game ${result.game.GameId}: ${result.ok?(deleting?'Deleted':'Updated'):result.error||responseError(result.response)}`));
   dialog.insertBefore(report,close);
   try{await onChanged();}catch{note.append(document.createTextNode(' Refresh the dashboard to verify the latest games.'));}
   finally{working=false;close.disabled=false;}
  };
 }catch(error){note.textContent=error.message||'Unable to check selected games. No changes made.';}
}
