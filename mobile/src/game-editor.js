import {loadCatalog,bindGameCatalog} from './catalog.js';
import {apiRequest} from './auth.js';
import {config} from './config.js';
import {gameURL,inputDate,inputTime,buildGame,responseError,gameFields} from './game-data.js';


function element(tag,text){const el=document.createElement(tag);if(text!==undefined)el.textContent=text;return el;}
async function request(url,method,data){const r=await apiRequest(url,method,data);if(r.status<200||r.status>=300){const error=new Error(responseError(r));error.status=r.status;error.data=r.data;throw error;}return r.data;}

export async function editGame(summary,onSaved){
 const creating=!summary;
 if(creating){
  const now=new Date(),date=`${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`;
  summary={GameId:'',Association:'',Date:date,Time:'6:00 PM',Sport:'',Level:'',Site:'',NumOfGames:'1',GameFee:'0.00',TravelPay:'0.00',AssignorFee:'0.00',TaxYear:String(now.getFullYear()),Status:'Pending',Assignor:'Unassigned',Officials:[]};
 }
 const dialog=element('dialog');dialog.className='game-editor';
 const title=element('h2',creating?'Add game':`Game ${summary.GameId} · ${summary.Association}`);dialog.append(title);
 const note=element('p','Loading game…');note.setAttribute('role','status');dialog.append(note);
 const close=element('button','Close');close.type='button';close.className='secondary';dialog.append(close);
 let working=false;
 const dismiss=()=>{if(working)return;dialog.close();dialog.remove();};close.onclick=dismiss;
 dialog.addEventListener('cancel',e=>{e.preventDefault();dismiss();});
 document.body.append(dialog);dialog.showModal();
 try{
  const url=creating?new URL('/api/mobile/game',config.apiBase).href:gameURL(config.apiBase,summary);
  const access=creating?await request(new URL('/api/mobile/access',config.apiBase).href):null;
  const snapshot=creating?{game:summary,...access}:await request(url);
  if(!dialog.isConnected)return;
  const game=snapshot.game;
  let [directory,catalog]=await Promise.all([request(new URL('/api/mobile/officials',config.apiBase).href),loadCatalog()]);
  if(!dialog.isConnected)return;
  const form=element('form');form.id='game-form';
  const inputs={};
  for(const [key,text,type='text'] of (creating?[['GameId','Game ID','number'],['Association','Association'],...gameFields]:[['Association','Association'],...gameFields])){
   const label=element('label',text),input=element(['Sport','Association','Assignor','Level','Site'].includes(key)?'select':'input');input.name=key;if(input.tagName==='INPUT')input.type=type;
   input.value=key==='Date'?inputDate(game[key]):key==='Time'?inputTime(game[key]):game[key]??'';
   if(['GameFee','TravelPay','AssignorFee'].includes(key))input.value=String(game[key]??'0').replace(/[$,]/g,'');
   input.required=['GameId','Association','Date','Time','Sport','Level','Site','NumOfGames','TaxYear','GameFee','TravelPay','AssignorFee'].includes(key);
   if(type==='number'){input.min=['GameId','NumOfGames'].includes(key)?'1':key==='TaxYear'?'2000':'0';input.step=['GameId','NumOfGames','TaxYear'].includes(key)?'1':'0.01';}
   if(key==='TaxYear')input.max='2100';
   if(key==='Date'){input.min='2000-01-01';input.max='2100-12-31';}
   inputs[key]=input;label.append(input);form.append(label);
  }
  const refreshBoundCatalog=bindGameCatalog(inputs,catalog,game,creating);
  const catalogLabels={sport:'Sport',level:'Level',site:'Site',assignor:'Assignor',association:'Association',official:'Official'};
  const addCatalogButton=(type,onClick)=>{
   const button=element('button',`+ New ${catalogLabels[type]}`);button.type='button';button.className='catalog-action secondary';button.addEventListener('click',onClick);return button;
  };
  const newCatalogAction=(type,origin)=>()=>openCatalogForm(type,null,origin);
  for(const key of ['Sport','Level','Site','Association','Assignor']){
   if(key==='Association'&&!creating)continue;
   const type=key.toLowerCase(),label=inputs[key].parentElement;
   if(snapshot.canWrite)label.append(addCatalogButton(type,newCatalogAction(type,inputs[key])));
  }
  inputs.Association.disabled=!creating;
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
    label.append(select);
    if(snapshot.canWrite)label.append(addCatalogButton('official',()=>openCatalogForm('official',assignment,select)));
    rolesBox.append(label);
   }
  };
  const loadRoles=async()=>{
   const version=++roleGeneration;rolesLoading=true;rolesFailed=false;save.disabled=true;
   if(!inputs.Sport.value){assignments=[];renderRoles();rolesLoading=false;note.textContent='Select a sport to load levels and official roles.';return;}
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
  async function reloadEditorCatalog(selections={}){
   const [nextCatalog,nextDirectory]=await Promise.all([
    loadCatalog(),
    request(new URL('/api/mobile/officials',config.apiBase).href)
   ]);
   catalog=nextCatalog;directory=nextDirectory||[];
   refreshBoundCatalog(catalog,selections);
   renderRoles();
  }
  function openCatalogForm(type,officialSlot,origin){
   if(working||!snapshot.canWrite)return;
   const names={official:'Official',site:'Site',assignor:'Assignor',association:'Association',level:'Level',sport:'Sport'};
   const definitions={
    official:[['firstName','First name','text',true],['lastName','Last name','text',true],['phone','Phone','tel',false],['email','Email','email',false],['address','Address','text',false]],
    site:[['name','Site name','text',true],['contact','Contact name','text',false],['phone','Phone','tel',false],['email','Email','email',false]],
    assignor:[['name','Assignor name','text',true]],
    association:[['id','Association ID','text',true],['name','Association name','text',true],['contact','Contact name','text',false],['phone','Phone','tel',false],['email','Email','email',false]],
    level:[['sport','Sport','select',true],['name','Level name','text',true],['gameFee','Game fee','number',true]],
    sport:[['sport','Sport','select',true]]
   };
   const child=element('dialog');child.className='catalog-editor';
   const childForm=element('form'),heading=element('h2',`New ${names[type]}`);
   const error=element('p');error.setAttribute('role','alert');error.className='catalog-error';
   const actions=element('div');actions.className='catalog-actions';
   const cancel=element('button','Cancel');cancel.type='button';cancel.className='secondary';
   const saveCatalog=element('button',type==='sport'?'Activate & select':'Save');saveCatalog.type='submit';
   const activate=element('button','Activate & select');activate.type='button';activate.hidden=true;
   const retryRefresh=element('button','Retry selection refresh');retryRefresh.type='button';retryRefresh.hidden=true;
   actions.append(cancel,retryRefresh,activate,saveCatalog);
   childForm.append(heading);
   if(type==='level'||type==='sport'){
    const help=element('p',type==='level'?'New levels are saved inactive and must be activated before they can be selected.':'Select a sport active in the shared catalog to activate it for this account.');
    help.className='catalog-help';childForm.append(help);
   }
   const controls={};
   for(const [name,labelText,inputType,required] of definitions[type]){
    const label=element('label',labelText);
    const input=element(inputType==='select'?'select':'input');input.name=name;
    if(input.tagName==='INPUT'){
     input.type=inputType;input.required=required;
     if(name==='gameFee'){input.min='0';input.step='0.01';input.value='0.00';}
     if(name==='name'&&type==='level')input.maxLength=50;
    }else{
     input.required=required;
     const choices=type==='sport'?catalog.availableSports||[]:catalog.sports;
     input.append(new Option(type==='sport'?'Select active sport':'Select sport',''));
     for(const sport of choices)input.append(new Option(sport,sport));
     if(type==='level'&&inputs.Sport.value)input.value=inputs.Sport.value;
     if(type==='sport'&&!choices.length)input.options[0].textContent='No available sports';
    }
    controls[name]=input;label.append(input);childForm.append(label);
   }
   if(type==='assignor'){
    const selected=inputs.Association.value;
    const help=element('p',selected?`This assignor will be added to ${selected}.`:'Select an association in the game form first.');
    help.className='catalog-help';childForm.append(help);
   }
   childForm.append(error,actions);child.append(childForm);document.body.append(child);
   let pendingLevel=null,createdValues=null,busy=false;
   const setError=message=>{error.textContent=message||'';};
   const setBusy=value=>{busy=value;for(const control of childForm.elements)control.disabled=value;};
   const close=(force=false)=>{if(busy&&!force)return;child.close();child.remove();origin?.focus();};
   cancel.onclick=close;
   child.addEventListener('cancel',event=>{event.preventDefault();close();});
   const catalogURL=action=>new URL(`/api/mobile/catalog/${action}/${type}`,config.apiBase).href;
   const applySaved=async()=>{
    if(type==='official'){
     await reloadEditorCatalog();
     const id=String(createdValues?.id??'');
     const byID=id&&directory.find(item=>String(item.Id??item.id??item.OfficialId??item.officialId)===id);
     const official=byID||directory.find(item=>
      String(item.FirstName??item.firstName??'').trim().toLowerCase()===String(controls.firstName.value).trim().toLowerCase()&&
      String(item.LastName??item.lastName??'').trim().toLowerCase()===String(controls.lastName.value).trim().toLowerCase());
     const officialID=String(official?.Id??official?.id??official?.OfficialId??official?.officialId??id);
     if(!officialID)throw new Error('Official was saved but could not be selected. Reopen the editor to refresh the officials list.');
     const first=String(official?.FirstName??official?.firstName??controls.firstName.value).trim();
     const last=String(official?.LastName??official?.lastName??controls.lastName.value).trim();
     if(officialSlot){officialSlot.officialId=officialID;officialSlot.name=`${first} ${last}`.trim();renderRoles();}
    }else{
     const field=type==='site'?'Site':type==='association'?'Association':type==='assignor'?'Assignor':type==='sport'?'Sport':undefined;
     const value=type==='association'?createdValues?.id:type==='assignor'||type==='site'?createdValues?.name:type==='sport'?createdValues?.sport:undefined;
     const selection=field?{[field]:value??''}:{};
     await reloadEditorCatalog(selection);
     if(type==='association')inputs.Association.dispatchEvent(new Event('change'));
     if(type==='sport')inputs.Sport.dispatchEvent(new Event('change'));
    }
    close(true);
   };
   retryRefresh.onclick=async()=>{
    if(busy)return;setBusy(true);setError('');
    try{await applySaved();}catch(refreshError){setError(`The catalog record was saved, but the game selections could not be refreshed: ${refreshError.message}`);setBusy(false);}
   };
   const activateLevel=async()=>{
    if(busy||!pendingLevel)return;
    setBusy(true);setError('Activating level…');
    try{
     await request(new URL('/api/mobile/catalog/activate/level',config.apiBase).href,'POST',pendingLevel);
     createdValues={Level:pendingLevel.name};
     await reloadEditorCatalog({Level:pendingLevel.name});
     inputs.Level.dispatchEvent(new Event('change'));
     close(true);
    }catch(activationError){setError(activationError.message);setBusy(false);}
   };
   activate.onclick=activateLevel;
   childForm.onsubmit=async event=>{
    event.preventDefault();if(busy||!childForm.reportValidity())return;
    const values=Object.fromEntries(new FormData(childForm));
    if(type==='assignor'&&!inputs.Association.value){setError('Select an association before creating an assignor.');return;}
    setError('');setBusy(true);
    try{
     if(type==='level'){
      try{
       pendingLevel=await request(catalogURL('create'), 'POST', values);
      }catch(createError){
       const duplicate=createError.status===409?createError.data?.level:null;
       if(!duplicate)throw createError;
       if(!duplicate.active){
        pendingLevel=duplicate;saveCatalog.hidden=true;activate.hidden=false;
        setError(`${createError.message} This matching level is inactive; activate it to use it.`);
       }else{
        setError(`${createError.message} This matching level is already active and can be selected in the game form.`);
       }
       setBusy(false);return;
      }
      saveCatalog.hidden=true;activate.hidden=false;
      setError('Level saved as inactive. Activate it to select it for this game.');
      setBusy(false);return;
     }
     if(type==='sport'){
      await request(catalogURL('create'),'POST',values);
      createdValues={sport:values.sport};
     }else{
      const payload=type==='assignor'?{association:inputs.Association.value,name:values.name}:values;
      const record=await request(catalogURL('create'),'POST',payload);
      createdValues=type==='official'?{id:record?.Id??record?.id,firstName:values.firstName,lastName:values.lastName}:values;
     }
     try{await applySaved();}
     catch(refreshError){
      saveCatalog.hidden=true;retryRefresh.hidden=false;
      setError(`Saved successfully, but selections could not be refreshed: ${refreshError.message}`);
      setBusy(false);
     }
    }catch(saveError){setError(saveError.message);setBusy(false);}
   };
   child.showModal();childForm.querySelector('input,select')?.focus();
   if(type==='assignor'&&!inputs.Association.value)setError('Select an association before creating an assignor.');
   if((type==='sport'||type==='level')&&!controls.sport.options.length)setError('No eligible sport options are available.');
  }
  inputs.Sport.onchange=loadRoles;
  const notesLabel=element('label','Notes'),notes=element('textarea');notes.name='Notes';notes.value=game.Notes||'';notes.maxLength=snapshot.notesLimit;notes.rows=4;notesLabel.className='wide';notesLabel.append(notes);form.append(notesLabel);
  const warning=element('p','Dropdowns use your account settings. Choose a level to fill its game fee. Record payments on the website. Cancelling a game clears all fees.');warning.className='wide';form.append(warning);
  const save=element('button',creating?'Add game':'Save changes');save.type='submit';form.append(save);
  const remove=element('button','Delete game');remove.type='button';remove.className='danger';remove.hidden=creating;form.append(remove);
  dialog.insertBefore(form,close);
  const lock=value=>{working=value;for(const el of form.elements)el.disabled=value;inputs.Association.disabled=value||!creating;close.disabled=value;};
  const finish=async text=>{dialog.close();dialog.remove();await onSaved(text);};
  form.onsubmit=async e=>{
   e.preventDefault();if(working||rolesLoading||rolesFailed||!form.reportValidity())return;
   const values=Object.fromEntries(new FormData(form));
   if(values.Status==='Cancelled'&&game.Status!=='Cancelled'&&!confirm('Cancel this game and set all fees to zero?'))return;
   lock(true);note.textContent='Saving changes…';
   try{await request(url,creating?'POST':'PUT',{game:buildGame(game,values,assignments),...(creating?{}:{version:snapshot.version})});await finish(creating?'Game added successfully. Adjust the filters if it is not shown.':'Game updated successfully.');}
   catch(error){note.textContent=error.message;lock(false);}
  };
  remove.onclick=async()=>{
   if(working||!confirm(`Permanently delete game ${game.GameId} for ${game.Association}?`))return;
   lock(true);note.textContent='Deleting game…';
   try{await request(url,'DELETE',{version:snapshot.version});await finish('Game deleted successfully.');}
   catch(error){note.textContent=error.message;lock(false);}
  };
  await loadRoles();
  if(game.Status==='Paid'||!snapshot.canWrite){for(const el of form.elements)el.disabled=true;note.textContent=game.Status==='Paid'?'Paid games cannot be edited or deleted.':'This account has read-only access.';}
 }catch(error){note.textContent=error.message;}
}
