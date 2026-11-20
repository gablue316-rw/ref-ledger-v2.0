import {apiRequest,apiGet} from './auth.js';
import {config} from './config.js';
import {assignmentEarnings} from './data.js';
import {responseError} from './game-data.js';

const valueOf=(record,...names)=>{
 for(const name of names)if(record?.[name]!==undefined&&record[name]!==null)return record[name];
 return '';
};
const textError=response=>Array.isArray(response.data?.errors)?response.data.errors.join('\n'):responseError(response);
const money=value=>new Intl.NumberFormat('en-US',{style:'currency',currency:'USD'}).format(Number(value)||0);
const taxYearValid=value=>/^[1-9][0-9]{3}$/.test(String(value));
const gameIdsValid=value=>/^\s*\d+(?:\s*-\s*\d+)?(?:\s*,\s*\d+(?:\s*-\s*\d+)?)*\s*$/.test(value);
const localDate=()=>{const now=new Date();return `${now.getFullYear()}-${String(now.getMonth()+1).padStart(2,'0')}-${String(now.getDate()).padStart(2,'0')}`;};
function normalizedGameIds(value){
 const source=Array.isArray(value)?value:[value];
 const ids=[];
 for(const part of source.flatMap(item=>String(item??'').split(/[;,]/))){
  const text=part.trim();
  if(!text)continue;
  const range=/^(\d+)\s*[-–—]\s*(\d+)$/.exec(text);
  if(!range){ids.push(Number(text));continue;}
  const first=Number(range[1]),last=Number(range[2]),step=first<=last?1:-1;
  if(Math.abs(last-first)+1>10000)throw new Error('The payment contains too many game IDs to display on this device.');
  for(let id=first;id!==last+step;id+=step)ids.push(id);
 }
 return ids.filter(Number.isSafeInteger);
}

export function createPayments(root){
 root.innerHTML=`
  <section class="mobile-panel">
   <h3>Record a payment</h3>
   <p>Only completed games are available. Game earnings are allocated by the server; a payment must equal selected-game earnings minus any deduction.</p>
   <form id="mobile-payment-form" class="mobile-page-form">
    <label>Payment ID<input name="paymentId" required maxlength="100"></label>
    <label>Payment date<input name="date" id="mobile-payment-date" type="date" required></label>
    <label>Tax Year<input name="taxYear" id="mobile-payment-tax-year" inputmode="numeric" maxlength="4" required></label>
    <label>Association<select name="association" id="mobile-payment-association" required><option value="">Select association</option></select></label>
    <label>Deduction amount<input name="deductionAmount" id="mobile-payment-deduction" type="number" min="0" step="0.01" value="0.00"></label>
    <label>Deduction type<select name="deductionType" id="mobile-payment-deduction-type"><option value="">Select type when deduction is positive</option></select></label>
    <label>Payment amount<input name="amount" id="mobile-payment-amount" type="number" min="0" step="0.01" required></label>
    <p class="wide">Expected deposit: <strong id="mobile-payment-expected">$0.00</strong></p>
    <div class="wide"><strong>Completed games available for payment</strong><p id="mobile-payment-games-message" class="mobile-message" role="status"></p><div id="mobile-payment-games" class="mobile-choice-list"></div></div>
    <div class="mobile-actions wide"><button type="submit" id="mobile-payment-save">Record payment</button></div>
   </form>
  </section>
  <section class="mobile-panel">
   <h3>Payment history</h3>
   <form id="mobile-payment-filter" class="mobile-page-form">
    <label>Payment ID<input name="paymentId"></label>
    <label>Payment date<input name="date" type="date"></label>
    <label>Tax Year<input name="taxYear" inputmode="numeric" maxlength="4" placeholder="All years"></label>
    <label>Association<select name="association"><option value="">All associations</option></select></label>
    <label>Amount<input name="amount" type="number" min="0" step="0.01"></label>
    <label>Game IDs<input name="gameIdFilter" placeholder="1,2,3-5"></label>
    <div class="mobile-actions wide"><button type="submit">Search payments</button><button type="reset" class="secondary">Clear filters</button></div>
   </form>
   <p id="mobile-payment-history-message" class="mobile-message" role="status" aria-live="polite"></p>
   <div id="mobile-payment-history"></div>
  </section>
  <dialog id="mobile-payment-edit-dialog" class="mobile-dialog">
   <form id="mobile-payment-edit-form">
    <h3 class="wide">Edit payment</h3>
    <label>Payment ID<input name="paymentId" readonly></label>
    <label>Association<input name="association" readonly></label>
    <label>Payment date<input name="date" type="date" required></label>
    <label>Tax Year<input name="taxYear" inputmode="numeric" maxlength="4" required></label>
    <label>Payment amount<input name="amount" type="number" min="0" step="0.01" required></label>
    <label>Deduction amount<input name="deductionAmount" type="number" min="0" step="0.01" required></label>
    <label>Deduction type<select name="deductionType"><option value="">Select type when deduction is positive</option></select></label>
    <label>Game IDs<input name="gameIds" required></label>
    <p id="mobile-payment-edit-message" class="mobile-message mobile-error wide" role="alert"></p>
    <div class="mobile-actions wide"><button type="submit">Save payment</button><button type="button" id="mobile-payment-edit-cancel" class="secondary">Cancel</button></div>
   </form>
  </dialog>`;

 const message=root.querySelector('#mobile-payment-history-message');
 const gameMessage=root.querySelector('#mobile-payment-games-message');
 const gameList=root.querySelector('#mobile-payment-games');
 const form=root.querySelector('#mobile-payment-form');
 const filterForm=root.querySelector('#mobile-payment-filter');
 const editDialog=root.querySelector('#mobile-payment-edit-dialog');
 const editForm=root.querySelector('#mobile-payment-edit-form');
 const editMessage=root.querySelector('#mobile-payment-edit-message');
 let canWrite=false,busy=false,games=[],expenseTypes=[],initialized=false;
 let editVersion='',editOriginalAssociation='';
 const url=path=>new URL(path,config.apiBase).href;
 const setMessage=(element,text,type='')=>{element.textContent=text;element.className=`mobile-message${type?` mobile-${type}`:''}`;};
 const getSelectedGames=()=>[...gameList.querySelectorAll('input[type=checkbox]:checked')].map(input=>games.find(game=>String(game.GameId??game.gameId)===input.value)).filter(Boolean);

 function updateExpected(){
  const total=getSelectedGames().reduce((sum,game)=>sum+assignmentEarnings(game),0);
  const deduction=Number(form.elements.namedItem('deductionAmount').value)||0;
  const expected=total-deduction;
  root.querySelector('#mobile-payment-expected').textContent=money(expected);
  if(expected>=0)form.elements.namedItem('amount').value=expected.toFixed(2);
 }
 function renderEligibleGames(){
  gameList.replaceChildren();
  const association=form.elements.namedItem('association').value;
  const eligible=games.filter(game=>String(game.Status??game.status).toLowerCase()==='completed'&&(!association||String(game.Association??game.association)===association));
  if(!eligible.length){gameMessage.textContent='No completed games are available for this association.';return;}
  gameMessage.textContent=`${eligible.length} completed game assignment(s) available.`;
  for(const game of eligible){
   const label=document.createElement('label');label.className='mobile-choice';
   const checkbox=document.createElement('input');checkbox.type='checkbox';checkbox.value=String(game.GameId??game.gameId);checkbox.disabled=!canWrite;
   checkbox.addEventListener('change',updateExpected);
   const summary=document.createElement('span');
   summary.textContent=`${game.Date||game.date||'—'} · Game ${checkbox.value} · ${game.Association||game.association||'—'} · ${money(assignmentEarnings(game))}`;
   label.append(checkbox,summary);gameList.append(label);
  }
  updateExpected();
 }
 function addOptions(select,values,placeholder){
  const selected=select.value;
  select.replaceChildren(new Option(placeholder,''));
  for(const value of [...new Set(values.filter(Boolean))].sort((a,b)=>a.localeCompare(b)))select.add(new Option(value,value));
  if(selected&&[...select.options].some(option=>option.value===selected))select.value=selected;
 }
 function renderPayments(records){
  const target=root.querySelector('#mobile-payment-history');target.replaceChildren();
  if(!records.length){setMessage(message,'0 payments found — 0 games — $0.00 total.');return;}
  let total=0;
  let gameCount=0;
  records.forEach((payment,index)=>{
   const id=String(valueOf(payment,'PaymentId','PaymentID','paymentId','paymentid'));
   const association=String(valueOf(payment,'Association','association'));
   const date=String(valueOf(payment,'PaymentDate','paymentDate','Date','date'));
   const amount=Number(valueOf(payment,'PaymentAmt','paymentAmt','Amount','amount'))||0;
   const gameIds=valueOf(payment,'GameIds','GameIDs','gameIds','gameids');
   const ids=Array.isArray(gameIds)?gameIds.map(String):String(gameIds??'').split(/[;,]/).map(value=>value.trim()).filter(Boolean);
   total+=amount;gameCount+=ids.reduce((count,id)=>{
    const range=/^(\d+)\s*-\s*(\d+)$/.exec(id);
    return count+(range?Math.abs(Number(range[2])-Number(range[1]))+1:1);
   },0);
   const card=document.createElement('article');card.className='mobile-record';
   const heading=document.createElement('h3');heading.textContent=`Payment ${id}`;card.append(heading);
   const details=document.createElement('dl');
   const fields=[
    ['Payment date',date],['Tax year',valueOf(payment,'TaxYear','taxYear','Taxyear')],
    ['Association',association],['Amount',money(amount)],
    ['Deduction',money(Number(valueOf(payment,'DeductionAmount','deductionAmount'))||0)],
    ['Deduction type',valueOf(payment,'DeductionType','deductionType')||'—'],
    ['Game IDs',ids.join(', ')||'—']
   ];
   for(const [label,value] of fields){const term=document.createElement('dt');term.textContent=label;const detail=document.createElement('dd');detail.textContent=String(value||'—');details.append(term,detail);}
   card.append(details);
   const actions=document.createElement('div');actions.className='mobile-actions';
   const viewGames=document.createElement('button');viewGames.type='button';viewGames.className='secondary';viewGames.textContent='View games';
   viewGames.addEventListener('click',()=>loadPaymentGames(association,gameIds,card));
   actions.append(viewGames);
   if(canWrite){
    const edit=document.createElement('button');edit.type='button';edit.dataset.requiresWrite='true';edit.textContent='Edit';edit.addEventListener('click',()=>openEdit(payment,index));
    const remove=document.createElement('button');remove.type='button';remove.className='danger';remove.textContent='Delete';
    remove.dataset.requiresWrite='true';
    remove.addEventListener('click',()=>deletePayment(payment,id,association));
    actions.append(edit,remove);card.append(actions);
   }else card.append(actions);
   target.append(card);
  });
  setMessage(message,`${records.length} payment(s) found — ${gameCount} game id(s) — ${money(total)} total.`);
 }
 async function loadPaymentGames(association,rawGameIds,card){
  card.querySelector('.payment-game-details')?.remove();
  let gameIds;
  try{gameIds=new Set(normalizedGameIds(rawGameIds));}
  catch(error){setMessage(message,error.message||'Unable to display payment games.','error');return;}
  if(!gameIds.size){setMessage(message,'The payment does not contain any game IDs.','error');return;}
  const params=new URLSearchParams({status:'Paid',association});
  const details=document.createElement('div');details.className='payment-game-details';
  const status=document.createElement('p');status.className='mobile-message';status.textContent='Loading associated games…';details.append(status);card.append(details);
  try{
   const response=await apiGet(`${url('/api/mobile/dashboard')}?${params}`);
   let available=[];
   if(response.status===200&&Array.isArray(response.data))available=response.data;
   else if(!(response.status===404&&response.data?.message==='No games were found matching your search criteria.'))throw new Error(responseError(response));
   const normalizedAssociation=association.trim().toLowerCase();
   const matches=available.filter(game=>gameIds.has(Number(game.GameId??game.gameId))&&String(game.Association??game.association??'').trim().toLowerCase()===normalizedAssociation);
   status.textContent=matches.length
    ?`${matches.length} of ${gameIds.size} associated game(s) loaded.${matches.length<gameIds.size?' Some recorded games could not be loaded.':''}`
    :`No associated paid games could be loaded. ${gameIds.size} game ID(s) were recorded.`;
   for(const game of matches){
    const item=document.createElement('article');item.className='mobile-record';
    const heading=document.createElement('h4');heading.textContent=`${game.Date||game.date||'—'} · Game ${game.GameId??game.gameId}`;item.append(heading);
    const detail=document.createElement('p');
    const count=Number(game.NumOfGames??game.numOfGames??0);
    detail.textContent=`${game.Status||game.status||'—'} · ${game.Sport||game.sport||'—'} · ${game.Level||game.level||'—'} · ${game.Site||game.site||'—'} · ${Number.isFinite(count)?`${count} ${count===1?'game':'games'}`:'Game count unavailable'} · ${money(assignmentEarnings(game))}`;
    item.append(detail);details.append(item);
   }
  }catch(error){status.textContent=error.message||'Unable to load payment games.';status.classList.add('mobile-error');}
 }
 function openEdit(payment,index){
  const id=String(valueOf(payment,'PaymentId','PaymentID','paymentId','paymentid'));
  editVersion=String(payment.version??payment.Version??'');
  editOriginalAssociation=String(valueOf(payment,'Association','association'));
  const dateValue=String(valueOf(payment,'PaymentDate','paymentDate','Date','date'));
  const match=dateValue.match(/^(\d{1,2})\/(\d{1,2})\/(\d{4})$/);
  const date=/^\d{4}-\d{2}-\d{2}$/.test(dateValue)?dateValue:match?`${match[3]}-${match[1].padStart(2,'0')}-${match[2].padStart(2,'0')}`:'';
  editForm.elements.namedItem('paymentId').value=id;
  editForm.elements.namedItem('association').value=editOriginalAssociation;
  editForm.elements.namedItem('date').value=date;
  editForm.elements.namedItem('taxYear').value=String(valueOf(payment,'TaxYear','taxYear','Taxyear'));
  editForm.elements.namedItem('amount').value=String(Number(valueOf(payment,'PaymentAmt','paymentAmt','Amount','amount'))||0);
  editForm.elements.namedItem('deductionAmount').value=String(Number(valueOf(payment,'DeductionAmount','deductionAmount'))||0);
  editForm.elements.namedItem('deductionType').value=String(valueOf(payment,'DeductionType','deductionType')||'');
  const ids=valueOf(payment,'GameIds','GameIDs','gameIds','gameids');
  editForm.elements.namedItem('gameIds').value=Array.isArray(ids)?ids.join(', '):String(ids||'');
  editForm.dataset.index=String(index);editMessage.textContent='';editDialog.showModal();
 }
 async function loadHistory(){
  const data=new FormData(filterForm),params=new URLSearchParams();
  for(const key of ['paymentId','date','taxYear','association','amount','gameIdFilter']){
   const value=String(data.get(key)||'').trim();
   if(value)params.set(key,value);
  }
  const gameFilter=String(data.get('gameIdFilter')||'').trim();
  if(gameFilter&&!gameIdsValid(gameFilter))throw new Error('Enter Game IDs as single IDs, comma-separated IDs, ranges, or a combination such as 1965,1983,1992-1999.');
  const query=params.toString();
  const response=await apiRequest(`${url('/api/mobile/payments')}${query?'?'+query:''}`);
  if(response.status!==200)throw new Error(responseError(response));
  const records=Array.isArray(response.data)?response.data:response.data?.payments??response.data?.Payments;
  if(!Array.isArray(records))throw new Error('The payment registry returned an invalid response.');
  renderPayments(records);
 }
 async function load(){
  if(busy)return;
  busy=true;setMessage(message,'Loading payments…');root.querySelectorAll('button').forEach(button=>button.disabled=true);
  try{
   const [catalogResponse,typesResponse,gamesResponse]=await Promise.all([
    apiRequest(url('/api/mobile/catalog')),apiRequest(url('/api/mobile/expense-types')),
    apiGet(`${url('/api/mobile/dashboard')}?status=Completed`)
   ]);
   if(catalogResponse.status!==200)throw new Error(responseError(catalogResponse));
   if(typesResponse.status!==200)throw new Error(responseError(typesResponse));
   const associations=(catalogResponse.data.associations||[]).map(item=>item.id).filter(Boolean);
   addOptions(form.elements.namedItem('association'),associations,'Select association');
   addOptions(filterForm.elements.namedItem('association'),associations,'All associations');
   expenseTypes=(Array.isArray(typesResponse.data)?typesResponse.data:[]).map(type=>type.expenseType??type.ExpenseType??type.name??type.Name).filter(Boolean);
   const typeSelect=form.elements.namedItem('deductionType');
   const editType=editForm.elements.namedItem('deductionType');
   for(const select of [typeSelect,editType]){
    const previous=select.value;select.replaceChildren(new Option('Select type when deduction is positive',''));
    for(const type of expenseTypes)select.add(new Option(type,type));
    select.value=previous;
   }
   if(gamesResponse.status===200&&Array.isArray(gamesResponse.data))games=gamesResponse.data;
   else if(gamesResponse.status===404&&gamesResponse.data?.message==='No games were found matching your search criteria.')games=[];
   else throw new Error(responseError(gamesResponse));
   renderEligibleGames();
   await loadHistory();
   initialized=true;
  }catch(error){setMessage(message,error.message||'Unable to load payment data. Retry without leaving the page.','error');}
  finally{busy=false;syncWriteState();}
 }
 function syncWriteState(){
  root.querySelectorAll('#mobile-payment-form input,#mobile-payment-form select,#mobile-payment-save').forEach(control=>control.disabled=!canWrite||busy);
   gameList.querySelectorAll('input[type=checkbox]').forEach(control=>control.disabled=!canWrite||busy);
  root.querySelectorAll('#mobile-payment-filter input,#mobile-payment-filter select,#mobile-payment-filter button').forEach(control=>control.disabled=busy);
  root.querySelectorAll('.mobile-record .mobile-actions button').forEach(button=>button.disabled=busy||(!canWrite&&button.dataset.requiresWrite==='true'));
  editForm.querySelectorAll('input,select,button[type=submit]').forEach(control=>control.disabled=!canWrite||busy);
  root.querySelector('#mobile-payment-edit-cancel').disabled=busy;
 }
 async function deletePayment(payment,id,association){
  if(!window.confirm(`Permanently delete payment ${id} for ${association}?`))return;
  setMessage(message,`Deleting payment ${id}…`);
  const version=String(payment.version??payment.Version??'');
  const params=new URLSearchParams({paymentId:id,association,version});
  try{
   const response=await apiRequest(`${url('/api/mobile/payments')}?${params}`, 'DELETE');
   if(response.status!==200)throw new Error(responseError(response));
   setMessage(message,`Payment ${id} deleted.`,'success');
   await load();
  }catch(error){setMessage(message,error.message||'Unable to delete payment.','error');}
 }
 form.elements.namedItem('date').value=localDate();
 form.elements.namedItem('taxYear').value=form.elements.namedItem('date').value.slice(0,4);
 form.elements.namedItem('date').addEventListener('change',()=>{
  if(!form.elements.namedItem('taxYear').dataset.overridden)form.elements.namedItem('taxYear').value=form.elements.namedItem('date').value.slice(0,4);
 });
 form.elements.namedItem('taxYear').addEventListener('input',event=>event.target.dataset.overridden=event.target.value?'true':'');
 form.elements.namedItem('association').addEventListener('change',renderEligibleGames);
 form.elements.namedItem('deductionAmount').addEventListener('input',updateExpected);
 filterForm.addEventListener('submit',async event=>{event.preventDefault();setMessage(message,'Searching payments…');try{await loadHistory();}catch(error){setMessage(message,error.message||'Unable to search payments.','error');}});
 filterForm.addEventListener('reset',()=>setTimeout(()=>{if(initialized)loadHistory().catch(error=>setMessage(message,error.message||'Unable to load payments.','error'));},0));
 form.addEventListener('submit',async event=>{
  event.preventDefault();
  const selected=getSelectedGames();
  const association=form.elements.namedItem('association').value;
  const selectedAssociations=new Set(selected.map(game=>String(game.Association??game.association)));
  const amount=Number(form.elements.namedItem('amount').value);
  const deduction=Number(form.elements.namedItem('deductionAmount').value||0);
  const type=form.elements.namedItem('deductionType').value;
  const taxYear=form.elements.namedItem('taxYear').value.trim();
  if(!selected.length){setMessage(gameMessage,'Select at least one completed game before saving the payment.','error');return;}
  if(selectedAssociations.size!==1||!association||association!==[...selectedAssociations][0]){setMessage(gameMessage,'Selected games must all belong to the selected association.','error');return;}
  if(!taxYearValid(taxYear)){setMessage(gameMessage,'Enter a four-digit Tax Year between 1000 and 9999.','error');return;}
  if(!Number.isFinite(amount)||amount<0||!Number.isFinite(deduction)||deduction<0){setMessage(gameMessage,'Enter valid nonnegative payment and deduction amounts.','error');return;}
  if(deduction>0&&!type){setMessage(gameMessage,'Select a deduction type for a positive deduction.','error');return;}
  if(!form.reportValidity())return;
  const payload={date:form.elements.namedItem('date').value,taxYear:Number(taxYear),paymentid:form.elements.namedItem('paymentId').value.trim(),amount,deductionAmount:deduction,deductionType:type,association,gameids:selected.map(game=>Number(game.GameId??game.gameId))};
  if(!payload.paymentid){setMessage(gameMessage,'Enter a Payment ID.','error');return;}
  setMessage(gameMessage,'Saving payment…');
  root.querySelector('#mobile-payment-save').disabled=true;
  try{
   const response=await apiRequest(url('/api/mobile/payments'),'POST',payload);
   if(response.status!==201||response.data?.totalErrors>0)throw new Error(textError(response));
   form.reset();form.elements.namedItem('date').value=localDate();form.elements.namedItem('taxYear').value=form.elements.namedItem('date').value.slice(0,4);
   form.elements.namedItem('taxYear').dataset.overridden='';
   setMessage(gameMessage,'Payment recorded. Completed games and payment history have been refreshed.','success');
   await load();
  }catch(error){setMessage(gameMessage,error.message||'Unable to record payment. Your entries are unchanged; correct them or retry.','error');}
  finally{syncWriteState();}
 });
 editForm.elements.namedItem('date').addEventListener('change',event=>{
  if(!editForm.elements.namedItem('taxYear').dataset.overridden)editForm.elements.namedItem('taxYear').value=event.target.value.slice(0,4);
 });
 editForm.elements.namedItem('taxYear').addEventListener('input',event=>event.target.dataset.overridden=event.target.value?'true':'');
 editForm.addEventListener('submit',async event=>{
  event.preventDefault();
  const date=editForm.elements.namedItem('date').value,taxYear=editForm.elements.namedItem('taxYear').value.trim();
  const amount=Number(editForm.elements.namedItem('amount').value),deductionAmount=Number(editForm.elements.namedItem('deductionAmount').value||0);
  const deductionType=editForm.elements.namedItem('deductionType').value,gameText=editForm.elements.namedItem('gameIds').value.trim();
  if(!date||!taxYearValid(taxYear)||!Number.isFinite(amount)||amount<0||!Number.isFinite(deductionAmount)||deductionAmount<0){setMessage(editMessage,'Enter a date and valid nonnegative payment and deduction amounts, and a four-digit tax year.','error');return;}
  if(deductionAmount>0&&!deductionType){setMessage(editMessage,'Select a deduction type for a positive deduction.','error');return;}
  if(!gameIdsValid(gameText)){setMessage(editMessage,'Enter Game IDs as a single ID, comma-separated IDs, ranges, or a combination such as 1965,1983,1992-1999.','error');return;}
  const gameids=[];
  for(const part of gameText.split(',')){
   const [start,end]=part.trim().split('-').map(Number);
   if(end===undefined){gameids.push(start);continue;}
   if(start>end){setMessage(editMessage,'Game ID ranges must be in ascending order.','error');return;}
   if(end-start+1>10000){setMessage(editMessage,'A game ID range cannot exceed 10,000 IDs.','error');return;}
   for(let id=start;id<=end;id++)gameids.push(id);
  }
  setMessage(editMessage,'Saving payment…');editForm.querySelector('button[type=submit]').disabled=true;
  try{
   const response=await apiRequest(url('/api/mobile/payments'),'PUT',{
    version:editVersion,paymentid:editForm.elements.namedItem('paymentId').value,
    originalAssociation:editOriginalAssociation,date,association:editOriginalAssociation,
    amount,taxYear:Number(taxYear),deductionAmount,deductionType,gameids
   });
   if(response.status!==200)throw new Error(responseError(response));
   editDialog.close();setMessage(message,'Payment updated.','success');await load();
  }catch(error){setMessage(editMessage,error.message||'Unable to update payment. Your entries are unchanged; correct them or retry.','error');}
  finally{editForm.querySelector('button[type=submit]').disabled=!canWrite;}
 });
 root.querySelector('#mobile-payment-edit-cancel').addEventListener('click',()=>editDialog.close());
 return {load,setCanWrite(value){canWrite=value;syncWriteState();},clear(){
  games=[];initialized=false;form.reset();filterForm.reset();gameList.replaceChildren();root.querySelector('#mobile-payment-history').replaceChildren();
  form.elements.namedItem('date').value=localDate();form.elements.namedItem('taxYear').value=form.elements.namedItem('date').value.slice(0,4);
  editDialog.close();setMessage(message,'');setMessage(gameMessage,'');
 }};
}
