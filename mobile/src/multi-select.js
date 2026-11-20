export function selectionSummary(values,allLabel){
 return values.length===0?allLabel:values.length===1?values[0]:`${values.length} selected`;
}
export function updateSelection(values,value,checked,selectAll=false){
 if(selectAll)return [];
 const next=new Set(values);
 if(checked)next.add(value);else next.delete(value);
 return [...next];
}

export function createMultiSelect(root,{name,label,values=[],selected=[],allLabel='All',sortValues}){
 const details=document.createElement('details');
 details.className='multi-select';
 const summary=document.createElement('summary');
 summary.textContent=`${label}: ${allLabel}`;
 const options=document.createElement('div');
 options.className='multi-select-options';
 const hidden=document.createElement('div');
 hidden.hidden=true;
 root.replaceChildren(details,hidden);
 details.append(summary,options);
 let choices=[...new Set(values.filter(Boolean))];
 let selectedValues=new Set(selected.filter(Boolean));

 function sync(){
  const selectedChoices=[...selectedValues];
  summary.textContent=`${label}: ${selectionSummary(selectedChoices,allLabel)}`;
  hidden.replaceChildren(...selectedChoices.map(value=>{
   const input=document.createElement('input');
   input.type='hidden';input.name=name;input.value=value;
   return input;
  }));
 }
 function render(){
  for(const value of selectedValues)if(!choices.includes(value))choices.push(value);
  choices.sort(sortValues||((a,b)=>a.localeCompare(b)));
  options.replaceChildren();
  const all=document.createElement('label');
  const allInput=document.createElement('input');
  allInput.type='checkbox';allInput.checked=selectedValues.size===0;
  allInput.addEventListener('change',()=>{if(allInput.checked){selectedValues=new Set(updateSelection([...selectedValues],'',false,true));render();}});
  all.append(allInput,document.createTextNode(allLabel));options.append(all);
  for(const value of choices){
   const item=document.createElement('label');
   const input=document.createElement('input');
   input.type='checkbox';input.value=value;input.checked=selectedValues.has(value);
   input.addEventListener('change',()=>{
    selectedValues=new Set(updateSelection([...selectedValues],value,input.checked));
    render();
   });
   item.append(input,document.createTextNode(value));options.append(item);
  }
  sync();
 }
 render();
 return {
  getValues:()=>[...selectedValues],
  clear:()=>{selectedValues.clear();render();},
  setValues:(nextValues)=>{selectedValues=new Set(nextValues.filter(Boolean));render();},
  setOptions:(nextValues)=>{choices=[...new Set(nextValues.filter(Boolean))];render();},
 };
}
