import test from 'node:test';
import assert from 'node:assert/strict';
// Catalog data helpers have no dependency on native authentication.
import {levelOptions,assignorOptions} from './catalog-data.js';
const catalog={levels:[{sport:'Softball',name:'Varsity',active:true,gameFee:'65.50'},{sport:'Basketball',name:'Varsity',active:true,gameFee:'75.00'},{sport:'Softball',name:'Retired',active:false}],associations:[{id:'MSO',assignors:['Jane Smith']},{id:'MCBOA',assignors:['John Doe']}]};
test('levels follow sport and hide inactive options without losing fee precision',()=>{
 assert.deepEqual(levelOptions(catalog,'Softball').map(l=>l.gameFee),['65.50']);
 assert.deepEqual(levelOptions(catalog,'Basketball').map(l=>l.gameFee),['75.00']);
 assert.equal(levelOptions(catalog,'Volleyball').length,0);
});
test('assignors belong to the selected association',()=>{
 assert.deepEqual(assignorOptions(catalog,'MSO'),['Unassigned','Jane Smith']);
 assert.deepEqual(assignorOptions(catalog,'MCBOA'),['Unassigned','John Doe']);
 assert.deepEqual(assignorOptions(catalog,''),['Unassigned']);
});
