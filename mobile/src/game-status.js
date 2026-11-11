import {apiRequest} from './auth.js';
import {config} from './config.js';
import {gameURL,responseError} from './game-data.js';
import {runBulkWrites} from './game-bulk-data.js';
export const editableStatuses=['Pending','Completed','Cancelled'];
const element=(tag,text)=>{const node=document.createElement(tag);if(text!==undefined)node.textContent=text;return node;};
// Status shortcuts use the same per-game authorization and version checks as editing.
export async function changeGameStatuses(games,status,onChanged){
 if(!games.length||!editableStatuses.includes(status))return;
 const target=games.length===1?`game ${games[0].GameId} for ${games[0].Association}`:`all ${games.length} selected games`;
 if(!confirm(`Set ${target} to ${status}?${status==='Cancelled'?' All fees will be set to zero.':''}`))return;
 const dialog=element('dialog');dialog.className='game-editor';dialog.setAttribute('aria-label','Update game status');
 dialog.append(element('h2',`Set status to ${status}`));
 const note=element('p','Checking assignments…');note.setAttribute('role','status');dialog.append(note);
 const close=element('button','Close');close.type='button';close.className='secondary';close.disabled=true;dialog.append(close);
 let working=true;const dismiss=()=>{if(!working){dialog.close();dialog.remove();}};close.onclick=dismiss;dialog.addEventListener('cancel',e=>{e.preventDefault();dismiss();});
 document.body.append(dialog);dialog.showModal();
 try{
  const snapshots=[];
  for(let i=0;i<games.length;i+=4){
   const group=await Promise.all(games.slice(i,i+4).map(async game=>{
    const response=await apiRequest(gameURL(config.apiBase,game));
    if(response.status!==200)throw new Error(responseError(response));return response.data;
   }));snapshots.push(...group);
  }
  if(snapshots.some(s=>!s.canWrite))throw new Error('This account has read-only access. No games changed.');
  if(snapshots.some(s=>s.game.Status==='Paid'))throw new Error('A game is now Paid. Refresh and select unpaid games. No games changed.');
  const results=await runBulkWrites(snapshots,'PUT',game=>({...game,Status:status}),
   (game,method,data)=>apiRequest(gameURL(config.apiBase,game),method,data),
   (count,total)=>{note.textContent=`Updating status: ${count} of ${total}…`;});
  const succeeded=results.filter(r=>r.ok).length;
  note.textContent=`${succeeded} set to ${status}; ${results.length-succeeded} not confirmed updated.`;
  const report=element('ul');report.className='bulk-results';
  for(const r of results)report.append(element('li',`${r.game.Association} · Game ${r.game.GameId}: ${r.ok?status:r.error||responseError(r.response)}`));dialog.insertBefore(report,close);
  await onChanged();
 }catch(error){note.textContent=error.message||'Unable to change status. Refresh to verify the latest games.';}
 finally{working=false;close.disabled=false;}
}
