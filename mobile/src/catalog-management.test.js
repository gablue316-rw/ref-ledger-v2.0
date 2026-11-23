import test from 'node:test';
import assert from 'node:assert/strict';
import {createCatalogManagement} from './catalog-management.js';

class Node {
 constructor(tag){this.tagName=tag.toUpperCase();this.children=[];this.value='';this.hidden=false;this.disabled=false;}
 append(...nodes){for(const node of nodes){node.parent=this;this.children.push(node);}}
 replaceChildren(...nodes){this.children=[];this.append(...nodes);}
 setAttribute(){}
 addEventListener(){}
 showModal(){}
 close(){}
 focus(){}
 remove(){if(this.parent)this.parent.children=this.parent.children.filter(child=>child!==this);}
 reportValidity(){return true;}
 get elements(){return this.walk().filter(node=>['INPUT','BUTTON'].includes(node.tagName));}
 walk(){return this.children.flatMap(node=>[node,...node.walk()]);}
 querySelector(selector){return this.walk().find(node=>selector==='input'&&node.tagName==='INPUT');}
}
globalThis.document={body:new Node('body'),createElement:tag=>new Node(tag)};
globalThis.FormData=class {constructor(form){this.entries=form.elements.filter(input=>input.tagName==='INPUT'&&!input.disabled).map(input=>[input.name,input.value]);} [Symbol.iterator](){return this.entries[Symbol.iterator]();}};

const button=(node,text)=>node.walk().find(item=>item.tagName==='BUTTON'&&item.textContent===text);
const form=()=>document.body.walk().find(node=>node.tagName==='FORM');
const input=(node,key)=>node.elements.find(item=>item.name===key);
const submit=async node=>node.onsubmit({preventDefault(){}});

test('catalog saves, preserves failed input, refreshes selectors, and retains blocked rows',async()=>{
 document.body.replaceChildren();
 let rows=[],failSave=true,blocked=true,refreshes=0,confirmText='';
 globalThis.confirm=text=>{confirmText=text;return true;};
 const send=async(url,method='GET',data)=>{
  if(method==='GET')return rows.map(row=>({...row}));
  if(method==='POST'){
   if(failSave)throw new Error('Site already exists');
   rows.push({...data,version:'v1'});return;
  }
  if(method==='PUT'){rows[0]={...rows[0],...data,version:'v2'};return;}
  if(blocked)throw new Error('Site is in use');
  rows=[];return;
 };
 const root=new Node('div'),catalog=createCatalogManagement('site',root,async()=>{refreshes++;},send);
 catalog.setCanWrite(true);await catalog.load();
 button(root,'Add site').onclick();
 let editor=form();input(editor,'name').value='Central';input(editor,'phone').value='555';
 await submit(editor);
 assert.equal(input(editor,'name').value,'Central');
 assert.equal(editor.walk().find(node=>node.className==='catalog-error').textContent,'Site already exists');
 failSave=false;await submit(editor);
 assert.equal(refreshes,1);
 assert.ok(root.walk().some(node=>node.tagName==='H3'&&node.textContent==='Central'));
 button(root,'Edit').onclick();editor=form();input(editor,'phone').value='123';await submit(editor);
 assert.equal(rows[0].phone,'123');assert.equal(refreshes,2);
 button(root,'Edit').onclick();editor=form();input(editor,'phone').value='discard';button(editor,'Cancel').onclick();
 assert.equal(rows[0].phone,'123');
 await button(root,'Delete').onclick();
 assert.equal(confirmText,'Delete Central?');
 assert.ok(root.walk().some(node=>node.tagName==='H3'&&node.textContent==='Central'));
 assert.ok(root.walk().some(node=>node.textContent==='Site is in use'));
 blocked=false;await button(root,'Delete').onclick();
 assert.equal(rows.length,0);assert.equal(refreshes,3);
 catalog.setCanWrite(false);
 assert.equal(button(root,'Add site').hidden,true);
});

test('catalog load errors remain retryable',async()=>{
 let attempts=0;
 const root=new Node('div'),catalog=createCatalogManagement('association',root,async()=>{},async()=>{if(!attempts++)throw new Error('Offline');return [];});
 await catalog.load();
 assert.ok(root.walk().some(node=>node.textContent==='Offline'));
 await button(root,'Retry').onclick();
 assert.ok(root.walk().some(node=>node.textContent==='No associations found.'));
});

test('read-only catalogs hide write actions and in-flight saves cannot repeat',async()=>{
 document.body.replaceChildren();
 let resolveSave,writes=0;
 const root=new Node('div'),catalog=createCatalogManagement('official',root,async()=>{},async(url,method='GET')=>{
  if(method==='GET')return [{id:1,firstName:'Ann',lastName:'Smith',version:'v1'}];
  writes++;return new Promise(resolve=>{resolveSave=resolve;});
 });
 catalog.setCanWrite(false);await catalog.load();
 assert.ok(root.walk().some(node=>node.textContent==='Ann Smith'));
 assert.equal(button(root,'Add official').hidden,true);
 assert.equal(button(root,'Edit'),undefined);assert.equal(button(root,'Delete'),undefined);
 catalog.setCanWrite(true);button(root,'Add official').onclick();
 const editor=form();input(editor,'firstName').value='Bob';input(editor,'lastName').value='Smith';
 const pending=submit(editor);await submit(editor);
 assert.equal(writes,1);
 resolveSave();await pending;
});
