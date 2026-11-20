import test from 'node:test';
import assert from 'node:assert/strict';
import {expenseInput,expenseValue} from './expense-data.js';
import {dashboardURL} from './data.js';
test('expense entry preserves cents and whole miles separately',()=>{
 const base={Date:'11/15/2026',Type:'Equipment',Amount:'$1,234.56',Mileage:'0',Association:'MSO',Description:'Shoes',TaxYear:'2026'};
 assert.deepEqual(expenseInput(base),{date:'2026-11-15',type:'Equipment',value:'1234.56',association:'MSO',description:'Shoes',taxYear:'2026'});
 const mileage={...base,Type:'Mileage',Amount:'$0.00',Mileage:'83'};
 const values=expenseInput(mileage);
 assert.equal(values.value,'83');assert.equal('gameId' in values,false);assert.equal(expenseValue(mileage),'83 miles');
});
test('game queries include new fields but never accept a tenant selector',()=>{
 const url=new URL(dashboardURL('https://example.com',{gameId:'42',site:'Central',level:'Varsity',home:'A',visitor:'B',tenantId:'other'}));
 assert.equal(url.searchParams.get('gameId'),'42');assert.equal(url.searchParams.get('site'),'Central');assert.equal(url.searchParams.get('level'),'Varsity');assert.equal(url.searchParams.get('home'),'A');assert.equal(url.searchParams.get('visitor'),'B');assert.equal(url.searchParams.has('tenantId'),false);
});
