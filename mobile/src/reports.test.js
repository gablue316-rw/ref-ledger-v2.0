import {test} from 'node:test';
import assert from 'node:assert/strict';
import {summarizeReport} from './reports.js';

test('game report summaries count assignments and their stored game counts',()=>{
 const report=summarizeReport('game',[
  {GameId:1,NumOfGames:1,TotalEarnings:'$25.00'},
  {GameId:2,NumOfGames:3,TotalEarnings:'$75.50'}
 ]);
 assert.equal(report.rows.length,2);
 assert.deepEqual(report.totals,[['Assignments displayed','2'],['Total number of games','4'],['Total assignment earnings','$100.50']]);
});

test('report summary totals mirror web calculations for financial and 1099 reports',()=>{
 assert.deepEqual(summarizeReport('financial',[
  {grossIncome:'$125.50',totalExpenses:'$20.25',netIncome:'$105.25',totalMileage:12},
  {grossIncome:'$10.00',totalExpenses:'$2.00',netIncome:'$8.00',totalMileage:3}
 ]).totals,[
  ['Earnings Before Payroll Deductions','$135.50'],
  ['Total Expenses','$22.25'],
  ['Net Income Before Taxes','$113.25'],
  ['Total Mileage','15']
 ]);
 assert.deepEqual(summarizeReport('1099',[{taxYear:'2025',association:'A',amount:'125.50'},{taxYear:'2025',association:'B',amount:'1.25'}]).totals,[['Grand Total','$126.75']]);
});

test('reports with no data have no fabricated totals',()=>{
 for(const type of ['game','financial','revenue','1099','expense','accounts-receivable','reconciliation']){
  assert.deepEqual(summarizeReport(type,[]),{rows:[],totals:[]});
 }
});
