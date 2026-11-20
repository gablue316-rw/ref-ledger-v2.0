import {apiRequest} from './auth.js';
import {config} from './config.js';
import {createMultiSelect} from './multi-select.js';
import {responseError} from './game-data.js';

const reportTypes=[
 ['game','Game Report'],['financial','Financial Report'],['revenue','Revenue Report'],
 ['1099','1099 Report'],['expense','Expense Report'],
 ['accounts-receivable','Accounts Receivable'],['reconciliation','Reconciliation Report']
];
const gameFilters=['status','sport','assignor','site','level','official','eco'];

function optionName(record){
 if(typeof record==='string')return record.trim();
 const first=record?.firstName??record?.FirstName??'';
 const last=record?.lastName??record?.LastName??'';
 return String(record?.name??record?.Name??`${first} ${last}`).trim();
}
function optionNames(records){
 return (Array.isArray(records)?records:[]).map(optionName).filter(Boolean);
}
export function officialNameComparator(records){
 const names=new Map((Array.isArray(records)?records:[]).map(record=>{
  const name=optionName(record);
  const first=String(record?.firstName??record?.FirstName??'').trim();
  const last=String(record?.lastName??record?.LastName??'').trim();
  const parts=name.split(/\s+/);
  return [name,{first:first||parts.slice(0,-1).join(' '),last:last||parts[parts.length-1]||''}];
 }));
 return (a,b)=>{
  const left=names.get(a)||{first:'',last:a};
  const right=names.get(b)||{first:'',last:b};
  return left.last.localeCompare(right.last,undefined,{sensitivity:'base'})||
   left.first.localeCompare(right.first,undefined,{sensitivity:'base'})||
   a.localeCompare(b);
 };
}
function displayValue(value){
 if(value===null||value===undefined)return '—';
 if(typeof value==='object')return JSON.stringify(value);
 return String(value);
}
export function isVisibleReportField(type,key){
 const field=String(key).toLowerCase();
 return !(type==='game'&&field==='version')&&
  !(type==='reconciliation'&&(field==='linkedgames'||field==='unlinkedgames'));
}
export function appendAssociationParams(params,associations){
 for(const association of associations)params.append('association',association);
}
export function reportFieldValue(type,key,value){
 if(type==='game'&&String(key).toLowerCase()==='officials'){
  if(Array.isArray(value)){
   const names=value.map(official=>{
    if(typeof official==='string')return official.trim();
    if(!official||typeof official!=='object')return '';
    const name=official.Name??official.name;
    if(name)return String(name).trim();
    return `${official.FirstName??official.firstName??''} ${official.LastName??official.lastName??''}`.trim();
   }).filter(Boolean);
   return names.join(', ')||'Unassigned';
  }
  return value?String(value):'Unassigned';
 }
 if(type==='1099'&&String(key).toLowerCase()==='amount')return currency(reportNumber(value));
 return displayValue(value);
}
function reportNumber(value){
 if(typeof value==='number')return Number.isFinite(value)?value:0;
 const text=String(value??'').trim(),negative=text.startsWith('(')&&text.endsWith(')');
 const number=Number.parseFloat(text.replace(/[^0-9.-]/g,''));
 if(!Number.isFinite(number))return 0;
 return negative?-Math.abs(number):number;
}
const currency=value=>new Intl.NumberFormat('en-US',{style:'currency',currency:'USD'}).format(value);
const mileage=value=>new Intl.NumberFormat('en-US',{maximumFractionDigits:0}).format(value);
const field=(row,...keys)=>{for(const key of keys)if(row?.[key]!==undefined&&row[key]!==null)return row[key];return '';};
function reportRows(report,type){
 if(type==='1099'&&report&&!Array.isArray(report)&&typeof report==='object'&&'association' in report)return [report];
 if(type==='revenue'&&report&&!Array.isArray(report)&&typeof report==='object'&&'association' in report)return [report];
 if(Array.isArray(report))return report;
 if(report&&typeof report==='object'){
  if(Array.isArray(report.data))return report.data;
  return Object.values(report);
 }
 return [];
}
function reportTotals(type,rows){
 if(!rows.length)return [];
 const totals=[];
 const sum=(label,key)=>rows.reduce((value,row)=>value+reportNumber(row?.[key]),0);
 const moneyFields={
  financial:[['Earnings Before Payroll Deductions','grossIncome'],['Total Expenses','totalExpenses'],['Net Income Before Taxes','netIncome']],
  revenue:[['Total Game Fees','totGameFees'],['Total Travel Pay','totTravelPay'],['Gross Earnings','grossRevenue'],['Total Assignor Fees','totAssignorFees'],['Payroll Deductions','payrollDeductions'],['Net Revenue','netRevenue']],
  expense:[['Food','food'],['Dues','dues'],['Camp Fees','campFees'],['Equipment','equipment'],['Payroll Deductions','payrollDeductions'],['Total Expenses','totalExpenses']],
  reconciliation:[['Payment Amount','paymentAmt'],['Calculated Amount','calculatedAmt']]
 };
 if(type==='game'){
  const games=rows.reduce((total,row)=>total+Math.max(0,reportNumber(field(row,'numOfGames','NumOfGames'))||1),0);
  const earnings=rows.reduce((total,row)=>total+reportNumber(field(row,'totalEarnings','TotalEarnings')),0);
  return [['Assignments displayed',String(rows.length)],['Total number of games',String(games)],['Total assignment earnings',currency(earnings)]];
 }
 if(type==='financial'||type==='revenue'||type==='expense'||type==='reconciliation'){
  for(const [label,key] of moneyFields[type])totals.push([label,currency(sum(label,key))]);
  const mileageKey=type==='financial'?'totalMileage':type==='expense'?'mileage':'';
  if(mileageKey)totals.push([type==='financial'?'Total Mileage':'Mileage',mileage(sum('',mileageKey))]);
  if(type==='revenue')totals.unshift(['Number of Games',new Intl.NumberFormat('en-US').format(sum('','numOfGames'))]);
  return totals;
 }
 if(type==='accounts-receivable')return [['Accounts receivable',currency(sum('','accountsReceivable'))]];
 if(type==='1099'){
  const cents=rows.reduce((total,row)=>total+Math.round(reportNumber(row.amount)*100),0);
  return [['Grand Total',currency(cents/100)]];
 }
 return [];
}
export function summarizeReport(type,report){
 const rows=reportRows(report,type).filter(row=>row&&typeof row==='object');
 return {rows,totals:reportTotals(type,rows)};
}
function labelFor(key){
 return key.replace(/([a-z])([A-Z])/g,'$1 $2').replace(/[-_]/g,' ').replace(/\b\w/g,char=>char.toUpperCase());
}

export function createReports(root){
 root.innerHTML=`
  <form id="mobile-report-form" class="mobile-page-form">
   <label>Report<select name="reportType" id="mobile-report-type"></select></label>
   <div data-report-filter="association"></div>
   <label id="report-taxyear-wrap">Tax Year<input name="taxYear" id="report-taxyear" inputmode="numeric" maxlength="4" placeholder="All years"></label>
   <div id="game-report-filters" class="wide" hidden>
    <div class="mobile-page-form">
     <div data-report-filter="status"></div><div data-report-filter="sport"></div>
     <div data-report-filter="assignor"></div><div data-report-filter="site"></div>
     <div data-report-filter="level"></div><div data-report-filter="official"></div>
     <div data-report-filter="eco"></div>
     <label>From<input type="date" name="begindate"></label>
     <label>Through<input type="date" name="enddate"></label>
     <label>Home<input name="home" autocomplete="off"></label>
     <label>Visitor<input name="visitor" autocomplete="off"></label>
    </div>
   </div>
   <div class="mobile-actions wide"><button type="submit" id="run-mobile-report">Run report</button><button type="reset" class="secondary">Clear filters</button></div>
  </form>
  <p id="mobile-report-message" class="mobile-message" role="status" aria-live="polite"></p>
  <div id="mobile-report-results" aria-live="polite"></div>`;

 const form=root.querySelector('#mobile-report-form');
 const typeInput=root.querySelector('#mobile-report-type');
 const message=root.querySelector('#mobile-report-message');
 const output=root.querySelector('#mobile-report-results');
 const runButton=root.querySelector('#run-mobile-report');
 for(const [value,title] of reportTypes)typeInput.add(new Option(title,value));
 let catalogLoaded=false,busy=false;
 let compareOfficialOptions=(a,b)=>a.localeCompare(b);
 const controls={};
 const create=(name,values,label)=>controls[name]=createMultiSelect(root.querySelector(`[data-report-filter="${name}"]`),{
  name,label,values,allLabel:name==='status'?'All statuses':`All ${label.toLowerCase()}`,
  sortValues:name==='official'||name==='eco'?(a,b)=>compareOfficialOptions(a,b):undefined
 });
 create('association',[],'Associations');
 for(const name of gameFilters)create(name,[],name==='eco'?'ECO':name==='status'?'Status':labelFor(name));
 controls.status.setOptions(['Pending','Completed','Paid','Cancelled']);

 function updateSections(){
  const game=typeInput.value==='game';
  root.querySelector('#game-report-filters').hidden=!game;
  root.querySelector('#report-taxyear-wrap').hidden=typeInput.value==='accounts-receivable';
 }
 typeInput.addEventListener('change',updateSections);
 updateSections();
 form.addEventListener('reset',()=>setTimeout(()=>{
  for(const control of Object.values(controls))control.clear();
  updateSections();
 },0));

 function renderReport(report){
  output.replaceChildren();
  const type=typeInput.value;
  const {rows,totals}=summarizeReport(type,report);
  if(!rows.length){
   const empty=document.createElement('p');empty.className='mobile-panel';empty.textContent='No report data matches the selected filters and period.';output.append(empty);return;
  }
  if(type==='game')rows.sort((a,b)=>{
   const dateA=new Date(field(a,'gameDateTime','GameDateTime','date','Date')||0).getTime();
   const dateB=new Date(field(b,'gameDateTime','GameDateTime','date','Date')||0).getTime();
   if(dateA!==dateB)return dateA-dateB;
   return String(field(a,'gameId','gameID','GameId','GameID')).localeCompare(String(field(b,'gameId','gameID','GameId','GameID')),undefined,{numeric:true});
  });
  else rows.sort((a,b)=>String(a.association??'').localeCompare(String(b.association??''))||String(a.taxYear??'').localeCompare(String(b.taxYear??''),undefined,{numeric:true}));
  if(totals.length){
   const summary=document.createElement('article');summary.className='mobile-record';
   const heading=document.createElement('h3');heading.textContent=type==='game'?'Game report summary':'Grand totals';summary.append(heading);
   const list=document.createElement('dl');
   for(const [name,value] of totals){const term=document.createElement('dt');term.textContent=name;const detail=document.createElement('dd');detail.textContent=value;list.append(term,detail);}
   summary.append(list);output.append(summary);
  }
  rows.forEach((row,index)=>{
   const card=document.createElement('article');card.className='mobile-record';
   const heading=document.createElement('h3');
   heading.textContent=String(row?.association??row?.Association??row?.gameId??row?.GameId??`${reportTypes.find(item=>item[0]===type)?.[1]||'Report'} row ${index+1}`);
   card.append(heading);
   if(row&&typeof row==='object'){
    const list=document.createElement('dl');
    for(const [key,value] of Object.entries(row)){
     if(!isVisibleReportField(type,key))continue;
     const term=document.createElement('dt');term.textContent=labelFor(key);
     const detail=document.createElement('dd');detail.textContent=reportFieldValue(type,key,value);
     list.append(term,detail);
    }
    card.append(list);
   }else{
    const text=document.createElement('p');text.textContent=displayValue(row);card.append(text);
   }
   output.append(card);
  });
 }

 async function load(){
  if(catalogLoaded)return;
  message.textContent='Loading report filters…';
  try{
   const [catalogResponse,officialResponse]=await Promise.all([
    apiRequest(new URL('/api/mobile/catalog',config.apiBase).href),
    apiRequest(new URL('/api/mobile/officials',config.apiBase).href)
   ]);
   if(catalogResponse.status!==200)throw new Error(responseError(catalogResponse));
   if(officialResponse.status!==200)throw new Error(responseError(officialResponse));
   const catalog=catalogResponse.data;
   if(!catalog||!Array.isArray(catalog.associations)||!Array.isArray(catalog.sports)||!Array.isArray(catalog.sites)||!Array.isArray(catalog.levels))throw new Error('Unable to load report filters.');
   controls.association.setOptions(catalog.associations.map(item=>item.id));
   controls.sport.setOptions(catalog.sports);
   controls.assignor.setOptions([...new Set(catalog.associations.flatMap(item=>['Unassigned',...(item.assignors||[])]))]);
   controls.site.setOptions(catalog.sites);
   controls.level.setOptions(catalog.levels.map(item=>item.name));
   const officials=optionNames(officialResponse.data);
   compareOfficialOptions=officialNameComparator(officialResponse.data);
   controls.official.setOptions(officials);
   controls.eco.setOptions(officials);
   catalogLoaded=true;
   message.textContent='';message.className='mobile-message';
  }catch(error){message.textContent=error.message||'Unable to load report filters. Retry by reopening Reports.';message.classList.add('mobile-error');}
 }

 function makeParams(){
  const params=new URLSearchParams();
  const type=typeInput.value;
  const formData=new FormData(form);
  const taxYear=String(formData.get('taxYear')||'').trim();
  if(type!=='accounts-receivable'&&taxYear&&!/^[1-9][0-9]{3}$/.test(taxYear))throw new Error('Tax Year must be a four-digit year from 1000 to 9999.');
  if(type!=='accounts-receivable'&&taxYear)params.set('taxYear',taxYear);
  appendAssociationParams(params,controls.association.getValues());
  if(type==='game'){
   const beginDate=String(formData.get('begindate')||'');
   const endDate=String(formData.get('enddate')||'');
   if(beginDate&&endDate&&beginDate>endDate)throw new Error('The through date must follow the from date.');
   for(const name of gameFilters)for(const value of controls[name].getValues())params.append(name,value);
   for(const name of ['begindate','enddate','home','visitor']){
    const value=String(formData.get(name)||'').trim();if(value)params.set(name,value);
   }
  }
  return params;
 }
 form.addEventListener('submit',async event=>{
  event.preventDefault();
  if(busy)return;
  let params;
  try{params=makeParams();}catch(error){message.textContent=error.message;message.className='mobile-message mobile-error';return;}
  busy=true;runButton.disabled=true;message.className='mobile-message';message.textContent='Generating report…';output.replaceChildren();
  try{
   const url=new URL(`/api/mobile/reports/${encodeURIComponent(typeInput.value)}`,config.apiBase);
   url.search=params.toString();
   const response=await apiRequest(url.href);
   if(response.status!==200)throw new Error(responseError(response));
   renderReport(response.data);
   message.textContent='';
  }catch(error){message.textContent=error.message||'Unable to generate report. Your filters are unchanged; retry when ready.';message.className='mobile-message mobile-error';}
  finally{busy=false;runButton.disabled=false;}
 });
 return {load,setCanWrite(){},clear(){
  catalogLoaded=false;form.reset();for(const control of Object.values(controls))control.clear();
  message.textContent='';message.className='mobile-message';output.replaceChildren();updateSections();
 }};
}
