import {gameKey} from './game-bulk-data.js';
const icons={edit:['M12 20h9','M16.5 3.5a2.1 2.1 0 0 1 3 3L9 17l-4 1 1-4Z'],delete:['M3 6h18','M8 6V4h8v2','M5 6l1 14h12l1-14','M10 10v6','M14 10v6']};
function iconButton(kind,label){
 const button=document.createElement('button');button.type='button';button.className=`icon-button ${kind==='delete'?'danger':'secondary'}`;button.title=label;button.setAttribute('aria-label',label);
 const ns='http://www.w3.org/2000/svg',svg=document.createElementNS(ns,'svg');svg.setAttribute('viewBox','0 0 24 24');svg.setAttribute('aria-hidden','true');
 for(const d of icons[kind]){const p=document.createElementNS(ns,'path');p.setAttribute('d',d);svg.append(p);}button.append(svg);return button;
}
export function createGameActions({container,onEdit,onDelete,onBulk}){
 const bar=document.createElement('section');bar.className='selection-bar';bar.setAttribute('aria-label','Selected games');
 const label=document.createElement('label'),all=document.createElement('input');all.type='checkbox';label.append(all,document.createTextNode('Select all unpaid games shown'));
 const count=document.createElement('span');count.setAttribute('role','status');
 const update=document.createElement('button');update.type='button';update.textContent='Update all selected';update.className='secondary';
 const remove=document.createElement('button');remove.type='button';remove.textContent='Delete all selected';remove.className='danger';bar.append(label,count,update,remove);container.before(bar);
 const selected=new Set(),boxes=new Map();let games=[],disabled=false;
 const chosen=()=>games.filter(g=>selected.has(gameKey(g)));
 const sync=()=>{
  const eligible=games.filter(g=>g.Status!=='Paid');
  all.checked=eligible.length>0&&selected.size===eligible.length;all.indeterminate=selected.size>0&&selected.size<eligible.length;all.disabled=disabled||!eligible.length;
  count.textContent=`${selected.size} selected`;update.disabled=remove.disabled=disabled||!selected.size;
  for(const [key,{box,card,paid}] of boxes){box.checked=selected.has(key);box.disabled=disabled||paid;card.classList.toggle('selected',box.checked);}
 };
 all.onchange=()=>{selected.clear();if(all.checked)for(const g of games)if(g.Status!=='Paid')selected.add(gameKey(g));sync();};
 update.onclick=()=>onBulk(chosen(),'PUT');remove.onclick=()=>onBulk(chosen(),'DELETE');
 return {
  reset(items=[]){games=items;selected.clear();boxes.clear();sync();},
  setDisabled(value){disabled=value;sync();for(const b of container.querySelectorAll('.icon-button'))b.disabled=disabled||b.dataset.paid==='true';},
  decorate(card,game){
   const key=gameKey(game),paid=game.Status==='Paid',row=document.createElement('div');row.className='game-actions';
   const label=document.createElement('label'),box=document.createElement('input');box.type='checkbox';box.setAttribute('aria-label',`Select game ${game.GameId} for ${game.Association}`);label.append(box,document.createTextNode(paid?'Paid · locked':'Select game'));row.append(label);
   boxes.set(key,{box,card,paid});box.onchange=()=>{if(box.checked)selected.add(key);else selected.delete(key);sync();};
   const edit=iconButton('edit',`Edit game ${game.GameId} for ${game.Association}`);edit.onclick=()=>onEdit(game);
   const remove=iconButton('delete',`Delete game ${game.GameId} for ${game.Association}`);remove.dataset.paid=String(paid);remove.disabled=paid;remove.onclick=()=>onDelete(game);row.append(edit,remove);card.prepend(row);sync();
  }
 };
}
