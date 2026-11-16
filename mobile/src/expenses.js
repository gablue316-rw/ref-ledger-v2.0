import {apiRequest} from './auth.js';
import {config} from './config.js';
import {inputDate,responseError} from './game-data.js';
import {expenseInput,expenseValue} from './expense-data.js';

const $=id=>document.getElementById(id);
function el(tag,text){const n=document.createElement(tag);if(text!==undefined)n.textContent=text;return n;}
async function request(url,method='GET',data){const r=await apiRequest(url,method,data);if(r.status<200||r.status>=300)throw new Error(responseError(r));return r.data;}
function address(record){const url=new URL('/api/mobile/expense',config.apiBase);if(record){url.searchParams.set('expenseId',record.expense.ExpenseId);url.searchParams.set('association',record.expense.Association);}return url.href;}

export function createExpenses(){
 let generation=0,canWrite=false,types=[],loading=false,dialog,clearing=false;
 const tell=text=>{$('expense-message').textContent=text;};
 function controls(){for(const node of $('expense-filters').elements)node.disabled=loading;$('refresh-expenses').disabled=loading;$('add-expense').disabled=loading||!canWrite;}
 async function load(success=''){
  const filters=new FormData($('expense-filters'));
  const current=++generation;loading=true;controls();tell('Loading expenses…');
  try{
   const url=new URL('/api/mobile/expenses',config.apiBase);
   for(const [key,value] of filters)if(value)url.searchParams.set(key,value);
   const data=await request(url.href);
   if(current!==generation)return;
   if(!Array.isArray(data.items)||!Array.isArray(data.types))throw new Error('Unable to read expenses.');
   canWrite=data.canWrite;types=data.types.map(t=>t.expenseType);
   const selected=$('expense-filter-type').value;
   $('expense-filter-type').replaceChildren(new Option('All types',''),...types.map(t=>new Option(t,t)));
   $('expense-filter-type').value=selected;
   $('expense-list').replaceChildren();
   data.items.sort((a,b)=>inputDate(b.expense.Date).localeCompare(inputDate(a.expense.Date))||a.expense.ExpenseId.localeCompare(b.expense.ExpenseId));
   for(const record of data.items){
    const e=record.expense,card=el('article');card.className='game';
    for(const [tag,text] of [['small',`${e.Type} · ${e.Association}`],['h2',e.Date],['strong',expenseValue(e)],['p',e.Description||'No description'],['p',`Tax year ${e.TaxYear} · Game ${e.GameId||'—'}`],['small',e.ExpenseId]])card.append(el(tag,text));
    const actions=el('div');actions.className='expense-actions';
    const edit=el('button',canWrite?'Edit':'View');edit.type='button';edit.onclick=()=>editor(record);actions.append(edit);
    if(canWrite){const remove=el('button','Delete');remove.type='button';remove.className='danger';remove.onclick=()=>removeExpense(record,remove);actions.append(remove);}
    card.append(actions);$('expense-list').append(card);
   }
   tell([success,`${data.items.length} expense${data.items.length===1?'':'s'} found.`].filter(Boolean).join(' '));
  }catch(error){if(current===generation){$('expense-list').replaceChildren();tell(error.message);}}
  finally{if(current===generation){loading=false;controls();}}
 }
 async function removeExpense(record,button){
  if(!confirm(`Permanently delete ${record.expense.Type} expense dated ${record.expense.Date}?`))return;
  const current=generation;button.disabled=true;
  try{await request(address(record),'DELETE',{version:record.version});if(current===generation)await load('Expense deleted successfully.');}
  catch(error){if(current===generation)tell(error.message);button.disabled=false;}
 }
 async function editor(record){
  if(dialog)return;
  const current=generation;
  dialog=el('dialog');const modal=dialog;modal.className='game-editor';
  modal.append(el('h2',record?'Expense details':'Add expense'));
  const note=el('p',record?'Loading expense…':'');note.setAttribute('role','status');modal.append(note);
  const close=el('button','Close');close.type='button';close.className='secondary';modal.append(close);
  let working=false;
  const dismiss=()=>{if(working)return;modal.close();modal.remove();if(dialog===modal)dialog=null;};
  close.onclick=dismiss;modal.addEventListener('cancel',e=>{e.preventDefault();dismiss();});
  document.body.append(modal);modal.showModal();
  try{
   const snapshot=record?await request(address(record)):null;
   if(!modal.isConnected||current!==generation)return;
   const e=snapshot?.expense;
   const now=new Date();const localDate=`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`;
   const values=e?expenseInput(e):{date:localDate,type:types[0],value:'0.00',association:'',gameId:'',description:'',taxYear:String(now.getFullYear())};
   const writable=snapshot?snapshot.canWrite:canWrite;
   const form=el('form'),inputs={};
   for(const [key,label,type] of [['date','Date','date'],['type','Type','select'],['value','Amount ($)','number'],['association','Association','text'],['gameId','Game ID (optional)','number'],['taxYear','Tax year','number'],['description','Description','textarea']]){
    const field=el('label',label);const input=el(type==='select'?'select':type==='textarea'?'textarea':'input');input.name=key;
    if(type==='select'){for(const t of new Set([...types,...(e?[e.Type]:[])]))input.append(new Option(t,t));}
    else if(type!=='textarea')input.type=type;
    input.value=values[key]??'';
    input.required=['date','type','value','association','taxYear'].includes(key);
    if(type==='number'){input.min=key==='taxYear'?'2000':'0';input.step=key==='value'?'0.01':'1';if(key==='taxYear')input.max='2100';}
    if(type==='date'){input.min='2000-01-01';input.max='2100-12-31';}
    if(type==='textarea'){input.maxLength=4000;input.rows=4;field.className='wide';}
    input.disabled=!writable;field.append(input);form.append(field);inputs[key]=input;
   }
   const valueLabel=inputs.value.parentElement;
   function valueMode(){valueLabel.firstChild.textContent=inputs.type.value==='Mileage'?'Whole miles':'Amount ($)';inputs.value.step=inputs.type.value==='Mileage'?'1':'0.01';}
   inputs.type.onchange=valueMode;valueMode();
   const hint=el('p','Use an association ID from your Ref Ledger account. Mileage records store miles; other types store dollars.');hint.className='wide';form.append(hint);
   const save=el('button',record?'Save changes':'Add expense');save.type='submit';save.disabled=!writable;form.append(save);
   modal.insertBefore(form,close);note.textContent=writable?'':'This account has read-only access.';
   form.onsubmit=async event=>{
    event.preventDefault();if(working||!writable||!form.reportValidity())return;
    const expense=Object.fromEntries(new FormData(form));
    working=true;close.disabled=true;for(const field of form.elements)field.disabled=true;note.textContent='Saving expense…';
    try{
     await request(record?address(record):address(),record?'PUT':'POST',{expense,...(record?{version:snapshot.version}:{})});
     working=false;dismiss();if(current===generation)await load(record?'Expense updated successfully.':'Expense added successfully. Adjust the filters if it is not shown.');
    }catch(error){note.textContent=error.message;working=false;close.disabled=false;for(const field of form.elements)field.disabled=false;}
   };
  }catch(error){note.textContent=error.message;}
 }
 $('expense-filters').onsubmit=e=>{e.preventDefault();load();};
 $('expense-filters').onreset=()=>{if(!clearing)queueMicrotask(()=>load());};
 $('refresh-expenses').onclick=()=>load();$('add-expense').onclick=()=>editor();
 return {load,clear(){++generation;loading=false;canWrite=false;types=[];if(dialog){dialog.close();dialog.remove();dialog=null;}$('expense-list').replaceChildren();clearing=true;$('expense-filters').reset();clearing=false;tell('');controls();}};
}
