import {apiRequest} from './auth.js';
import {config} from './config.js';
import {responseError} from './game-data.js';

const definitions={
 official:{title:'Official',fields:[['firstName','First name',true],['lastName','Last name',true],['phone','Phone'],['email','Email'],['address','Address']],identity:['firstName','lastName']},
 association:{title:'Association',fields:[['id','Association ID',true],['name','Association name'],['contact','Contact name'],['phone','Phone'],['email','Email'],['assignors','Assignors (comma-separated)']],identity:['id']},
 site:{title:'Site',fields:[['name','Site name',true],['contact','Contact name'],['phone','Phone'],['email','Email']],identity:['name']}
};
const element=(tag,text)=>{const node=document.createElement(tag);if(text!==undefined)node.textContent=text;return node;};
const name=(type,item)=>type==='official'?`${item.firstName} ${item.lastName}`.trim():item.name||item.id;
const id=(type,item)=>type==='site'?item.name:String(item.id);

async function request(url,method='GET',data){
 const result=await apiRequest(url,method,data);
 if(result.status<200||result.status>=300)throw new Error(responseError(result));
 return result.data;
}

export function createCatalogManagement(type,root,onChanged,send=request){
 const definition=definitions[type];
 let canWrite=false,busy=false,generation=0,records=[],dialog=null;
 const url=(action,item)=>{const target=new URL(`/api/mobile/catalog/${action}/${type}`,config.apiBase);if(item)target.searchParams.set('id',id(type,item));return target.href;};
 const message=element('p');message.className='mobile-message';message.setAttribute('role','status');
 const actions=element('div');actions.className='page-actions';
 const add=element('button',`Add ${definition.title.toLowerCase()}`);add.type='button';
 const refresh=element('button','Refresh');refresh.type='button';refresh.className='secondary';
 const list=element('div');
 actions.append(add,refresh);root.append(actions,message,list);
 const controls=()=>{add.hidden=!canWrite;add.disabled=busy;refresh.disabled=busy;};
 const setMessage=(text,error=false)=>{message.textContent=text;message.className=`mobile-message${error?' mobile-error':''}`;};
 const render=()=>{
  list.replaceChildren();
  for(const record of records){
   const card=element('article');card.className='mobile-record';
   card.append(element('h3',name(type,record)));
   if(canWrite){
    const buttons=element('div');buttons.className='mobile-actions';
    const edit=element('button','Edit');edit.type='button';edit.className='secondary';edit.disabled=busy;edit.onclick=()=>openForm(record);
    const remove=element('button','Delete');remove.type='button';remove.className='danger';remove.disabled=busy;remove.onclick=()=>removeRecord(record);
    buttons.append(edit,remove);card.append(buttons);
   }
   list.append(card);
  }
 };
 async function load(){
  if(busy||dialog)return;
  const current=++generation;busy=true;controls();list.replaceChildren();setMessage(`Loading ${definition.title.toLowerCase()}s…`);
  try{
   const data=await send(url('records'));
   if(current!==generation)return;
   if(!Array.isArray(data))throw new Error('Unable to read records. Please retry.');
   refresh.textContent='Refresh';records=data.sort((a,b)=>name(type,a).localeCompare(name(type,b)));
   setMessage(records.length?'':`No ${definition.title.toLowerCase()}s found.`);render();
  }catch(error){if(current===generation){records=[];refresh.textContent='Retry';setMessage(error.message||'Unable to load records. Please retry.',true);}}
  finally{if(current===generation){busy=false;controls();render();}}
 }
 async function changed(){
  const previous=generation;busy=false;
  await load();
  if(generation!==previous+1)return;
  const current=generation;
  try{await onChanged();}catch(error){if(current===generation)setMessage(`Record saved, but game filters could not be refreshed: ${error.message}`,true);}
 }
 async function removeRecord(record){
  if(busy||!canWrite||!confirm(`Delete ${name(type,record)}?`))return;
  const current=generation;
  busy=true;controls();render();setMessage('Deleting…');
  try{await send(url('records',record),'DELETE',{version:record.version});if(current===generation)await changed();}
  catch(error){if(current===generation)setMessage(error.message||'Unable to delete. Please retry.',true);}
  finally{if(canWrite){busy=false;controls();render();}}
 }
 function openForm(record){
  if(busy||dialog||!canWrite)return;
  dialog=element('dialog');dialog.className='catalog-editor';
  const editor=dialog,current=generation;
  const form=element('form');form.append(element('h2',`${record?'Edit':'Add'} ${definition.title.toLowerCase()}`));
  for(const [key,labelText,required] of definition.fields){
   const label=element('label',labelText),input=element('input');input.name=key;input.type=key==='email'?'email':key==='phone'?'tel':'text';
   input.required=!!required;input.readOnly=!!record&&definition.identity.includes(key);input.value=record?.[key]??'';
   label.append(input);form.append(label);
  }
  const error=element('p');error.className='catalog-error';error.setAttribute('role','alert');
  const buttons=element('div');buttons.className='catalog-actions';
  const cancel=element('button','Cancel');cancel.type='button';cancel.className='secondary';
  const save=element('button','Save');save.type='submit';buttons.append(cancel,save);form.append(error,buttons);dialog.append(form);document.body.append(dialog);
  let saving=false;
  const close=()=>{if(saving)return;editor.close();editor.remove();if(dialog===editor)dialog=null;};
  cancel.onclick=close;dialog.addEventListener('cancel',event=>{event.preventDefault();close();});
  form.onsubmit=async event=>{
   event.preventDefault();if(saving||!form.reportValidity())return;
   const values=Object.fromEntries([...new FormData(form)].map(([key,value])=>[key,value.trim()]));
   for(const [key,label,required] of definition.fields)if(required&&!values[key]){error.textContent=`${label} is required.`;return;}
   saving=true;busy=true;controls();render();for(const control of form.elements)control.disabled=true;error.textContent='Saving…';
   try{
    await send(url(record?'records':'create',record),record?'PUT':'POST',record?{...values,version:record.version}:values);
    saving=false;if(current!==generation)return;close();await changed();
   }catch(saveError){if(current!==generation)return;error.textContent=saveError.message||'Unable to save. Please retry.';saving=false;busy=false;controls();render();for(const control of form.elements)control.disabled=false;}
  };
  dialog.showModal();form.querySelector('input')?.focus();
 }
 add.onclick=()=>openForm(null);refresh.onclick=load;
 return {load,setCanWrite(value){canWrite=value;controls();render();},clear(){++generation;busy=false;canWrite=false;records=[];refresh.textContent='Refresh';if(dialog){dialog.close();dialog.remove();dialog=null;}list.replaceChildren();setMessage('');controls();}};
}
