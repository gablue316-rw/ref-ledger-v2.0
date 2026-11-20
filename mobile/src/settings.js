import {apiRequest} from './auth.js';
import {config} from './config.js';
import {responseError} from './game-data.js';

const collections=['associations','expenses','games','levels','officials','payments','sites'];
const url=path=>new URL(path,config.apiBase).href;
const feeText=value=>{const amount=Number(String(value??'0').replace(/[$,\s]/g,''));return Number.isFinite(amount)&&amount>=0?amount.toFixed(2):'0.00';};
const messageText=response=>typeof response.data==='string'?response.data.trim():response.data?.message||response.data?.error||responseError(response);

export function createSettings(root){
 root.innerHTML=`
  <p id="mobile-settings-message" class="mobile-message" role="status" aria-live="polite"></p>
  <section class="mobile-panel">
   <h3>Active Sports</h3><p>Choose the sports available to this account.</p>
   <div id="mobile-supported-sports" class="mobile-choice-list"></div>
   <div class="mobile-actions"><button type="button" id="mobile-save-sports">Save sports</button><button type="button" id="mobile-cancel-sports" class="secondary">Cancel</button></div>
  </section>
  <section class="mobile-panel">
   <h3>Levels</h3><p>Manage levels and game fees. New levels are created inactive and must be activated before selecting them on games.</p>
   <form id="mobile-level-form" class="mobile-page-form">
    <label>Sport<select name="sport" required><option value="">Select sport</option></select></label>
    <label>Level name<input name="name" maxlength="50" required></label>
    <label>Game fee<input name="gameFee" type="number" min="0" step="0.01" value="0.00" required></label>
    <div class="mobile-actions"><button type="submit">Add level</button></div>
   </form>
   <div id="mobile-levels" aria-live="polite"></div>
  </section>
  <section class="mobile-panel">
   <h3>Delete Records</h3>
   <p><strong>This cannot be undone.</strong> Selected records for this account are permanently removed. Related records may reference one another.</p>
   <div id="mobile-delete-collections" class="mobile-choice-list"></div>
   <label>Type DELETE to confirm<input id="mobile-delete-confirmation" autocomplete="off"></label>
   <button type="button" id="mobile-delete-records" class="danger">Delete selected records</button>
   <p id="mobile-delete-message" class="mobile-message" role="status"></p>
  </section>`;

 const message=root.querySelector('#mobile-settings-message');
 const supported=root.querySelector('#mobile-supported-sports');
 const levelForm=root.querySelector('#mobile-level-form');
 const levelsRoot=root.querySelector('#mobile-levels');
 const deleteMessage=root.querySelector('#mobile-delete-message');
 let canWrite=false,busy=false,sports=[],levels=[],savedSupported=new Set(),selectedSupported=new Set();
 const setMessage=(element,text,type='')=>{element.textContent=text;element.className=`mobile-message${type?` mobile-${type}`:''}`;};

 function syncWriteState(){
  const disabled=!canWrite||busy;
  root.querySelector('#mobile-save-sports').disabled=disabled;
  root.querySelector('#mobile-cancel-sports').disabled=busy;
  supported.querySelectorAll('input').forEach(input=>input.disabled=!canWrite||busy);
  levelForm.querySelectorAll('input,select,button').forEach(input=>input.disabled=disabled);
  root.querySelectorAll('.mobile-level-actions button').forEach(button=>button.disabled=disabled);
  const selected=deleteList&&deleteList.querySelector('input:checked');
  root.querySelector('#mobile-delete-records').disabled=disabled||!selected||root.querySelector('#mobile-delete-confirmation').value!=='DELETE';
  root.querySelector('#mobile-delete-confirmation').disabled=disabled;
  root.querySelectorAll('#mobile-delete-collections input').forEach(input=>input.disabled=disabled);
 }
 function renderSports(){
  supported.replaceChildren();
  for(const sport of sports.filter(item=>item.active&&item.name)){
   const label=document.createElement('label');label.className='mobile-choice';
   const checkbox=document.createElement('input');checkbox.type='checkbox';checkbox.value=sport.name;checkbox.checked=selectedSupported.has(sport.name);
   checkbox.addEventListener('change',()=>{if(checkbox.checked)selectedSupported.add(sport.name);else selectedSupported.delete(sport.name);});
   const name=document.createElement('span');name.textContent=sport.name;label.append(checkbox,name);supported.append(label);
  }
  if(!supported.childElementCount){const empty=document.createElement('p');empty.textContent='No active sports are available.';supported.append(empty);}
  syncWriteState();
 }
 function renderLevelSports(selected=''){
  const select=levelForm.elements.namedItem('sport');
  select.replaceChildren(new Option('Select sport',''));
  const activeSupported=sports.filter(sport=>sport.active&&selectedSupported.has(sport.name)).map(sport=>sport.name);
  for(const name of [...new Set(activeSupported)].sort((a,b)=>a.localeCompare(b)))select.add(new Option(name,name));
  if(selected&&!Array.from(select.options).some(option=>option.value===selected))select.add(new Option(`${selected} (saved value)`,selected));
  if(selected)select.value=selected;
 }
 function renderLevels(){
  levelsRoot.replaceChildren();
  if(!levels.length){const empty=document.createElement('p');empty.textContent='No levels have been configured.';levelsRoot.append(empty);return;}
  for(const level of levels){
   const card=document.createElement('article');card.className='mobile-record';
   const heading=document.createElement('h3');heading.textContent=`${level.sport||'Not assigned'} · ${level.name}`;card.append(heading);
   const detail=document.createElement('p');detail.textContent=`Game fee: $${feeText(level.gameFee)} · ${level.active?'Active':'Inactive'}`;card.append(detail);
   const actions=document.createElement('div');actions.className='mobile-actions mobile-level-actions';
   const toggle=document.createElement('button');toggle.type='button';toggle.className='secondary';toggle.textContent=level.active?'Deactivate':'Activate';
   toggle.addEventListener('click',()=>toggleLevel(level));actions.append(toggle);
   const edit=document.createElement('button');edit.type='button';edit.textContent='Edit';edit.addEventListener('click',()=>editLevel(card,level));actions.append(edit);
   const remove=document.createElement('button');remove.type='button';remove.className='danger';remove.textContent='Delete';
   remove.addEventListener('click',()=>deleteLevel(level));actions.append(remove);
   card.append(actions);levelsRoot.append(card);
  }
  syncWriteState();
 }
 function editLevel(card,level){
  const form=document.createElement('form');form.className='mobile-page-form';
  form.innerHTML='<label>Sport<select name="sport" required></select></label><label>Level name<input name="name" maxlength="50" required></label><label>Game fee<input name="gameFee" type="number" min="0" step="0.01" required></label><p class="mobile-message wide" role="status"></p><div class="mobile-actions wide"><button type="submit">Save level</button><button type="button" class="secondary">Cancel</button></div>';
  const select=form.elements.namedItem('sport');
  for(const sport of sports.filter(item=>item.name))select.add(new Option(sport.name,sport.name));
  if(level.sport&&!Array.from(select.options).some(option=>option.value===level.sport))select.add(new Option(level.sport,level.sport));
  select.value=level.sport||'';form.elements.namedItem('name').value=level.name;form.elements.namedItem('gameFee').value=feeText(level.gameFee);
  form.querySelector('button[type=button]').addEventListener('click',()=>form.remove());
  form.addEventListener('submit',async event=>{
   event.preventDefault();
   const name=form.elements.namedItem('name').value.trim(),gameFee=feeText(form.elements.namedItem('gameFee').value);
   if(!name||!form.reportValidity())return;
   await write(async()=>{
    const response=await apiRequest(url('/api/mobile/settings/levels'),'PUT',{version:level.version,id:level.id,name,sport:select.value,gameFee});
    if(response.status<200||response.status>=300)throw new Error(messageText(response));
    if(await load(true))setMessage(message,'Level saved successfully.','success');
   },form.querySelector('[role=status]'));
  });
  card.append(form);syncWriteState();
 }
 async function write(action,target=message){
  if(!canWrite||busy)return;
  busy=true;syncWriteState();setMessage(target,'Saving…');
  try{await action();}
  catch(error){setMessage(target,error.message||'Unable to save. Your values are unchanged; correct them or retry.','error');}
  finally{busy=false;syncWriteState();}
 }
 async function toggleLevel(level){
  const path=level.active?'level-inactive':'level-active';
  await write(async()=>{
   const response=await apiRequest(url(`/api/mobile/settings/${path}`),'POST',{version:level.version,id:level.id,name:level.name});
   if(response.status<200||response.status>=300)throw new Error(messageText(response));
   if(await load(true))setMessage(message,response.data?.message||`${level.name} is now ${level.active?'inactive':'active'}.`,'success');
  });
 }
 async function deleteLevel(level){
  if(!window.confirm(`Delete level ${level.name}? Levels used by games cannot be deleted.`))return;
  const params=new URLSearchParams({id:level.id,version:level.version});
  await write(async()=>{
   const response=await apiRequest(`${url('/api/mobile/settings/levels')}?${params}`,'DELETE');
   if(response.status<200||response.status>=300)throw new Error(messageText(response));
   if(await load(true))setMessage(message,`${level.name} deleted successfully.`,'success');
  });
 }
 async function load(force=false){
  if(busy&&!force)return false;
  setMessage(message,'Loading settings…');
  try{
   const [sportsResponse,supportedResponse,levelsResponse]=await Promise.all([
    apiRequest(url('/api/mobile/settings/sports')),
    apiRequest(url('/api/mobile/settings/supported-sports')),
    apiRequest(url('/api/mobile/settings/levels'))
   ]);
   for(const response of [sportsResponse,supportedResponse,levelsResponse])if(response.status!==200)throw new Error(messageText(response));
   sports=(Array.isArray(sportsResponse.data)?sportsResponse.data:[]).map(item=>({id:item.id??item.ID,name:item.name??item.Name,active:item.active??item.Active}));
   savedSupported=new Set(Array.isArray(supportedResponse.data)?supportedResponse.data:[]);
   selectedSupported=new Set(savedSupported);
   levels=(Array.isArray(levelsResponse.data)?levelsResponse.data:[]).map(level=>({...level,gameFee:level.gameFee??level.GameFee??0}));
   renderSports();renderLevelSports();renderLevels();setMessage(message,'');
   return true;
  }catch(error){setMessage(message,error.message||'Unable to load settings. Reopen this page to retry.','error');return false;}
 }
 root.querySelector('#mobile-save-sports').addEventListener('click',()=>write(async()=>{
  const selected=[...selectedSupported].sort((a,b)=>a.localeCompare(b));
  const response=await apiRequest(url('/api/mobile/settings/supported-sports'),'POST',selected);
  if(response.status<200||response.status>=300)throw new Error(messageText(response));
  savedSupported=new Set(selected);renderLevelSports();setMessage(message,'Supported sports saved.','success');
 }));
 root.querySelector('#mobile-cancel-sports').addEventListener('click',()=>{
  selectedSupported=new Set(savedSupported);renderSports();renderLevelSports();setMessage(message,'');
 });
 levelForm.addEventListener('submit',async event=>{
  event.preventDefault();
  const name=levelForm.elements.namedItem('name').value.trim(),sport=levelForm.elements.namedItem('sport').value;
  const fee=levelForm.elements.namedItem('gameFee');
  if(!name){setMessage(message,'Level name is required.','error');return;}
  if(!sport){setMessage(message,'Select a sport for this level.','error');return;}
  if(!fee.value)fee.value='0.00';
  if(!fee.checkValidity()){fee.reportValidity();return;}
  const gameFee=feeText(fee.value);fee.value=gameFee;
  await write(async()=>{
   const response=await apiRequest(url('/api/mobile/settings/levels'),'POST',{id:name,name,sport,gameFee});
   if(response.status!==201)throw new Error(messageText(response));
   const selectedSport=sport;levelForm.elements.namedItem('name').value='';fee.value='0.00';
   if(await load(true)){renderLevelSports(selectedSport);setMessage(message,'Level added inactive. Activate it before selecting it on a game.','success');}
  });
 });
 const deleteList=root.querySelector('#mobile-delete-collections');
 for(const collection of collections){
  const label=document.createElement('label');label.className='mobile-choice';
  const input=document.createElement('input');input.type='checkbox';input.name='collection';input.value=collection;
  const text=document.createElement('span');text.textContent=collection[0].toUpperCase()+collection.slice(1);
  label.append(input,text);deleteList.append(label);
 }
 deleteList.addEventListener('change',syncWriteState);
 root.querySelector('#mobile-delete-confirmation').addEventListener('input',syncWriteState);
 root.querySelector('#mobile-delete-records').addEventListener('click',()=>write(async()=>{
  const selected=[...deleteList.querySelectorAll('input:checked')].map(input=>input.value);
  if(!selected.length)throw new Error('Select at least one collection to delete.');
  const confirmation=root.querySelector('#mobile-delete-confirmation').value;
  if(confirmation!=='DELETE')throw new Error('Type DELETE to confirm permanent deletion.');
  if(!window.confirm(`Permanently delete all records in: ${selected.join(', ')}? This cannot be undone.`)){setMessage(deleteMessage,'Deletion cancelled.');return;}
  const response=await apiRequest(url('/api/mobile/settings/delete-records'),'POST',{collections:selected,confirmation},{},130000);
  if(response.status<200||response.status>=300)throw new Error(messageText(response));
  if(response.data?.success!==true)throw new Error('The server did not confirm deletion. Check your records before retrying.');
  deleteList.querySelectorAll('input').forEach(input=>input.checked=false);
  root.querySelector('#mobile-delete-confirmation').value='';
  setMessage(deleteMessage,`Records deleted from: ${selected.join(', ')}.`,'success');
  if(selected.includes('levels'))await load(true);
 }));
 return {load,setCanWrite(value){canWrite=value;syncWriteState();},clear(){
  sports=[];levels=[];savedSupported.clear();selectedSupported.clear();supported.replaceChildren();levelsRoot.replaceChildren();
  levelForm.reset();root.querySelector('#mobile-delete-confirmation').value='';
  deleteList.querySelectorAll('input').forEach(input=>input.checked=false);
  setMessage(message,'');setMessage(deleteMessage,'');
 }};
}
