import {test} from 'node:test';
import assert from 'node:assert/strict';
import {selectionSummary,updateSelection} from './multi-select.js';

test('multi-select presents All, one, and multiple selected values clearly',()=>{
 assert.equal(selectionSummary([],'All statuses'),'All statuses');
 assert.equal(selectionSummary(['Pending'],'All statuses'),'Pending');
 assert.equal(selectionSummary(['Pending','Completed'],'All statuses'),'2 selected');
});

test('multi-select supports starting, widening, removing, and clearing selections',()=>{
 let selected=[];
 selected=updateSelection(selected,'Pending',true);
 assert.deepEqual(selected,['Pending']);
 selected=updateSelection(selected,'Completed',true);
 assert.deepEqual(selected,['Pending','Completed']);
 selected=updateSelection(selected,'Pending',false);
 assert.deepEqual(selected,['Completed']);
 selected=updateSelection(selected,'Completed',false);
 assert.deepEqual(selected,[]);
 selected=updateSelection(['Softball'],'',false,true);
 assert.deepEqual(selected,[]);
});
