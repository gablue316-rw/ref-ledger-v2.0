import {levelOptions,assignorOptions} from './catalog-data.js';
import {apiRequest} from './auth.js';
import {config} from './config.js';
import {responseError} from './game-data.js';

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
 const preserve=!creating;
 fillOptions(inputs.Sport,catalog.sports,{placeholder:'Select sport',selected:saved.Sport||'',preserve});
 fillOptions(inputs.Site,catalog.sites,{placeholder:'Select site',selected:saved.Site||'',preserve});
 fillOptions(inputs.Association,catalog.associations.map(a=>a.id),{placeholder:'Select association',selected:saved.Association||'',preserve});
 const levels=(selected='',keep=false)=>fillOptions(inputs.Level,levelOptions(catalog,inputs.Sport.value).filter(l=>inputs.Sport.value&&l.sport===inputs.Sport.value).map(l=>l.name),{placeholder:inputs.Sport.value?'Select level':'Select sport first',selected,preserve:keep});
 const assignors=(selected='Unassigned',keep=false)=>fillOptions(inputs.Assignor,assignorOptions(catalog,inputs.Association.value),{placeholder:'Select assignor',selected,preserve:keep});
 levels(saved.Level||'',preserve);assignors(saved.Assignor||'Unassigned',preserve);
 inputs.Sport.addEventListener('change',()=>{const same=preserve&&inputs.Sport.value===saved.Sport;levels(same?saved.Level:'',same);inputs.GameFee.value=same?String(saved.GameFee).replace(/[$,]/g,''):'0.00';});
 inputs.Association.addEventListener('change',()=>assignors());
 inputs.Level.addEventListener('change',()=>{
  const level=levelOptions(catalog,inputs.Sport.value).find(l=>l.name===inputs.Level.value);
  if(level)inputs.GameFee.value=String(level.gameFee).replace(/[$,]/g,'');
 });
}
export function bindGameFilters(form,catalog){
 const keys=['sport','association','level','site','assignor'];
 const selected=Object.fromEntries(keys.map(key=>[key,form.elements.namedItem(key).value]));
 const inputs={};for(const key of keys)inputs[key]=asSelect(form.elements.namedItem(key));
 fillOptions(inputs.sport,catalog.sports,{placeholder:'All sports',selected:selected.sport,preserve:true});
 fillOptions(inputs.association,catalog.associations.map(a=>a.id),{placeholder:'All associations',selected:selected.association,preserve:true});
 fillOptions(inputs.site,catalog.sites,{placeholder:'All sites',selected:selected.site,preserve:true});
 const levels=(value='',preserve=false)=>fillOptions(inputs.level,levelOptions(catalog,inputs.sport.value).map(l=>l.name),{placeholder:'All levels',selected:value,preserve});
 levels(selected.level,true);inputs.sport.onchange=()=>levels();
 const assignors=(value='',preserve=false)=>fillOptions(inputs.assignor,inputs.association.value?assignorOptions(catalog,inputs.association.value):['Unassigned',...catalog.associations.flatMap(a=>a.assignors)],{placeholder:'All assignors',selected:value,preserve});
 assignors(selected.assignor,true);inputs.association.onchange=()=>assignors();
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
 for(const name of names){const input=form.elements.namedItem(name);if(input?.tagName==='SELECT')fillOptions(input,[],{placeholder:`All ${name==='association'?'associations':name==='sport'?'sports':name==='site'?'sites':name==='assignor'?'assignors':'levels'}`});}
}
