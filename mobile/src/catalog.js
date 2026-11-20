import {levelOptions,assignorOptions} from './catalog-data.js';
import {apiRequest} from './auth.js';
import {config} from './config.js';
import {responseError} from './game-data.js';
import {createMultiSelect} from './multi-select.js';

export async function loadCatalog(){
 const response=await apiRequest(new URL('/api/mobile/catalog',config.apiBase).href);
 if(response.status!==200)throw new Error(response.status===404?'Dropdown data is unavailable. Update the Ref Ledger backend, then reopen this screen.':responseError(response));
 const catalog=response.data;
 if(!catalog||!['sports','associations','sites','levels'].every(key=>Array.isArray(catalog[key])))throw new Error('Unable to read dropdown data. Reopen this screen to retry.');
 return catalog;
}
export function fillOptions(select,values,{placeholder='Select an option',selected='',preserve=false}={}){
 select.replaceChildren(new Option(placeholder,''));
 for(const value of new Set(values.filter(Boolean)))select.append(new Option(value,value));
 if(preserve&&selected&&![...select.options].some(option=>option.value===selected))select.append(new Option(`${selected} (saved value)`,selected));
 select.value=selected;
}
export function asSelect(input){
 if(input.tagName==='SELECT')return input;
 const select=document.createElement('select');
 for(const attr of input.attributes)if(!['type','value','placeholder'].includes(attr.name))select.setAttribute(attr.name,attr.value);
 input.replaceWith(select);return select;
}
export function bindGameCatalog(inputs,catalog,saved={},creating=false){
 let currentCatalog=catalog;
 const preserve=!creating;
 const levels=(selected='',keep=false)=>fillOptions(inputs.Level,levelOptions(currentCatalog,inputs.Sport.value).filter(l=>inputs.Sport.value&&l.sport===inputs.Sport.value).map(l=>l.name),{placeholder:inputs.Sport.value?'Select level':'Select sport first',selected,preserve:keep});
 const assignors=(selected='Unassigned',keep=false)=>fillOptions(inputs.Assignor,assignorOptions(currentCatalog,inputs.Association.value),{placeholder:'Select assignor',selected,preserve:keep});
 const refresh=(nextCatalog,current={})=>{
  currentCatalog=nextCatalog;
  const sport=current.Sport??(inputs.Sport.value||saved.Sport||'');
  const site=current.Site??(inputs.Site.value||saved.Site||'');
  const association=current.Association??(inputs.Association.value||saved.Association||'');
  fillOptions(inputs.Sport,currentCatalog.sports,{placeholder:'Select sport',selected:sport,preserve});
  fillOptions(inputs.Site,currentCatalog.sites,{placeholder:'Select site',selected:site,preserve});
  fillOptions(inputs.Association,currentCatalog.associations.map(a=>a.id),{placeholder:'Select association',selected:association,preserve});
  levels(current.Level??(inputs.Level.value||saved.Level||''),preserve);
  assignors(current.Assignor??(inputs.Assignor.value||saved.Assignor||'Unassigned'),preserve);
 };
 refresh(catalog,{Sport:saved.Sport||'',Site:saved.Site||'',Association:saved.Association||'',Level:saved.Level||'',Assignor:saved.Assignor||'Unassigned'});
 inputs.Sport.addEventListener('change',()=>{const same=preserve&&inputs.Sport.value===saved.Sport;levels(same?saved.Level:'',same);inputs.GameFee.value=same?String(saved.GameFee).replace(/[$,]/g,''):'0.00';});
 inputs.Association.addEventListener('change',()=>assignors());
 inputs.Level.addEventListener('change',()=>{
  const level=levelOptions(currentCatalog,inputs.Sport.value).find(l=>l.name===inputs.Level.value);
  if(level)inputs.GameFee.value=String(level.gameFee).replace(/[$,]/g,'');
 });
 return refresh;
}
export function bindGameFilters(form,catalog){
 const keys=['status','sport','association','assignor','site','level'];
 const labels={status:'Status',sport:'Sport',association:'Association',assignor:'Assignor',site:'Site',level:'Level'};
 const allLabels={status:'All statuses',sport:'All sports',association:'All associations',assignor:'All assignors',site:'All sites',level:'All levels'};
 const initial=new FormData(form);
 const options={
  status:['Pending','Completed','Paid','Cancelled'],
  sport:catalog.sports||[],
  association:(catalog.associations||[]).map(item=>item.id),
  assignor:[...new Set((catalog.associations||[]).flatMap(item=>['Unassigned',...(item.assignors||[])]))],
  site:catalog.sites||[],
  level:(catalog.levels||[]).map(item=>item.name)
 };
 const controls={};
 for(const key of keys){
  controls[key]=createMultiSelect(form.querySelector(`[data-filter="${key}"]`),{
   name:key,label:labels[key],allLabel:allLabels[key],values:options[key],
   selected:initial.getAll(key).length?initial.getAll(key):(key==='status'?['Pending']:[])
  });
 }
 return {
  clearAll(){for(const key of keys)controls[key].clear();},
  setValues(key,values){controls[key].setValues(values);},
  updateCatalog(next){
   options.sport=next.sports||[];
   options.association=(next.associations||[]).map(item=>item.id);
   options.assignor=[...new Set((next.associations||[]).flatMap(item=>['Unassigned',...(item.assignors||[])]))];
   options.site=next.sites||[];
   options.level=(next.levels||[]).map(item=>item.name);
   for(const key of keys)controls[key].setOptions(options[key]);
  }
 };
}
export function bindExpenseAssociation(input,catalog,value='',preserve=false){
 const select=asSelect(input);fillOptions(select,catalog.associations.map(a=>a.id),{placeholder:select.required?'Select association':'All associations',selected:value,preserve});return select;
}
export function bindBulkCatalog(inputs,catalog,games){
 const common=lists=>lists.length?lists[0].filter(value=>lists.every(list=>list.includes(value))):[];
 fillOptions(inputs.Sport,catalog.sports,{placeholder:'Select sport'});
 fillOptions(inputs.Site,catalog.sites,{placeholder:'Select site'});
 fillOptions(inputs.Assignor,common([...new Set(games.map(g=>g.Association))].map(a=>assignorOptions(catalog,a))),{placeholder:'Select assignor',selected:'Unassigned'});
 const levels=()=>{const sports=inputs.Sport.disabled?[...new Set(games.map(g=>g.Sport))]:[inputs.Sport.value];fillOptions(inputs.Level,common(sports.map(sport=>levelOptions(catalog,sport).filter(l=>sport&&l.sport===sport).map(l=>l.name))),{placeholder:'Select level'});};
 inputs.Sport.addEventListener('change',levels);inputs.Sport.closest('.bulk-field').querySelector('input[type=checkbox]').addEventListener('change',levels);levels();
}

export function clearFilterCatalog(form,names){
 for(const name of names){const input=form.querySelector(`[data-filter="${name}"]`);if(input)input.replaceChildren();}
}
