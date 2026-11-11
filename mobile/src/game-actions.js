import {editableStatuses} from './game-status.js';
import {gameKey} from './game-bulk-data.js';
const icons={complete:['M22 11.1V12a10 10 0 1 1-5.9-9.1','M22 4 12 14l-3-3'],edit:['M12 20h9','M16.5 3.5a2.1 2.1 0 0 1 3 3L9 17l-4 1 1-4Z'],delete:['M3 6h18','M8 6V4h8v2','M5 6l1 14h12l1-14','M10 10v6','M14 10v6']};
function iconButton(kind,label){
 const button=document.createElement('button');button.type='button';button.className=`icon-button ${kind==='delete'?'danger':kind==='complete'?'complete-action':'secondary'}`;button.title=label;button.setAttribute('aria-label',label);
 const ns='http://www.w3.org/2000/svg',svg=document.createElementNS(ns,'svg');svg.setAttribute('viewBox','0 0 24 24');svg.setAttribute('aria-hidden','true');
 for(const d of icons[kind]){const p=document.createElementNS(ns,'path');p.setAttribute('d',d);svg.append(p);}button.append(svg);return button;
}
export function createGameActions({container,onEdit,onDelete,onBulk,onStatus}){
 const bar=document.createElement('section');bar.className='selection-bar';bar.setAttribute('aria-label','Selected games');
 const label=document.createElement('label'),all=document.createElement('input');all.type='checkbox';label.append(all,document.createTextNode('Select all'));
 const count=document.createElement('span');count.setAttribute('role','status');
 const statusLabel=document.createElement('label');statusLabel.className='bulk-status-label';statusLabel.append(document.createTextNode('Update selected games to'));
 const status=document.createElement('select');status.setAttribute('aria-label','Status for selected games');status.append(new Option('Select status',''));for(const value of editableStatuses)status.append(new Option(value,value));statusLabel.append(status);
 const update=document.createElement('button');update.type='button';update.textContent='Update All Selected';update.className='status-update';
 const complete=document.createElement('button');complete.type='button';complete.textContent='Complete All Selected';complete.className='complete-action';
 const details=document.createElement('button');details.type='button';details.textContent='Edit Selected Details';details.className='secondary';
 const remove=document.createElement('button');remove.type='button';remove.textContent='Delete All Selected';remove.className='danger';bar.append(label,count,statusLabel,update,complete,details,remove);container.before(bar);
 const selected=new Set(),boxes=new Map();let games=[],disabled=false;
 const chosen=()=>games.filter(g=>selected.has(gameKey(g)));
 const sync=()=>{
  const eligible=games.filter(g=>g.Status!=='Paid');
  all.checked=eligible.length>0&&selected.size===eligible.length;all.indeterminate=selected.size>0&&selected.size<eligible.length;all.disabled=disabled||!eligible.length;
  count.textContent=`${selected.size} selected`;complete.disabled=details.disabled=remove.disabled=disabled||!selected.size;update.disabled=disabled||!selected.size||!status.value;status.disabled=disabled||!selected.size;
  for(const [key,{box,card,paid}] of boxes){box.checked=selected.has(key);box.disabled=disabled||paid;card.classList.toggle('selected',box.checked);}
 };
 all.onchange=()=>{selected.clear();if(all.checked)for(const g of games)if(g.Status!=='Paid')selected.add(gameKey(g));sync();};
 status.onchange=sync;
 update.onclick=()=>onStatus(chosen(),status.value);
 complete.onclick=()=>onStatus(chosen(),'Completed');
 details.onclick=()=>onBulk(chosen(),'PUT');remove.onclick=()=>onBulk(chosen(),'DELETE');
 return {
  reset(items=[]){games=items;status.value='';selected.clear();boxes.clear();sync();},
  setDisabled(value){disabled=value;sync();for(const b of container.querySelectorAll('.icon-button'))b.disabled=disabled||b.dataset.paid==='true'||b.dataset.completed==='true';for(const select of container.querySelectorAll('.game-status-select'))select.disabled=disabled||select.dataset.paid==='true';},
  decorate(card,game){
   const key=gameKey(game),paid=game.Status==='Paid',row=document.createElement('div');row.className='game-actions';
   const label=document.createElement('label'),box=document.createElement('input');box.type='checkbox';box.setAttribute('aria-label',`Select game ${game.GameId} for ${game.Association}`);label.append(box,document.createTextNode(paid?'Paid · locked':'Select game'));row.append(label);
   boxes.set(key,{box,card,paid});box.onchange=()=>{if(box.checked)selected.add(key);else selected.delete(key);sync();};
   const complete=iconButton('complete',`Complete game ${game.GameId} for ${game.Association}`);complete.dataset.paid=String(paid);complete.dataset.completed=String(game.Status==='Completed');complete.disabled=paid||game.Status==='Completed';complete.onclick=()=>onStatus([game],'Completed');
   const edit=iconButton('edit',`Edit game ${game.GameId} for ${game.Association}`);edit.onclick=()=>onEdit(game);
   const remove=iconButton('delete',`Delete game ${game.GameId} for ${game.Association}`);remove.dataset.paid=String(paid);remove.disabled=paid;remove.onclick=()=>onDelete(game);row.append(complete,edit,remove);card.prepend(row);
   const statusLabel=document.createElement('label');statusLabel.className='card-status';statusLabel.append(document.createTextNode('Status'));
   const status=document.createElement('select');status.className='game-status-select';status.setAttribute('aria-label',`Status for game ${game.GameId} for ${game.Association}`);
   for(const value of [...editableStatuses,...(paid?['Paid']:[])])status.append(new Option(value,value));status.value=game.Status;status.dataset.paid=String(paid);status.disabled=paid;
   status.onchange=async()=>{const requested=status.value;status.value=game.Status;status.disabled=true;try{await onStatus([game],requested);}finally{status.disabled=disabled||paid;}};
   statusLabel.append(status);row.after(statusLabel);sync();
  }
 };
}
