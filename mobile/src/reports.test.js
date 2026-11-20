import {test} from 'node:test';
import assert from 'node:assert/strict';
import {appendAssociationParams,isVisibleReportField,officialNameComparator,reportFieldValue,summarizeReport} from './reports.js';

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

test('game report fields hide Version and show only officials names',()=>{
 assert.equal(isVisibleReportField('game','version'),false);
 assert.equal(isVisibleReportField('game','GameId'),true);
 assert.equal(reportFieldValue('game','Officials',[
  {Name:'Zoe Adams',Title:'Referee',OfficialId:17},
  {name:'Amy Brown',title:'Umpire',officialId:18}
 ]),'Zoe Adams, Amy Brown');
});

test('reconciliation report hides Linked Games and Unlinked Games',()=>{
 assert.equal(isVisibleReportField('reconciliation','linkedGames'),false);
 assert.equal(isVisibleReportField('reconciliation','unlinkedGames'),false);
 assert.equal(isVisibleReportField('reconciliation','paymentGameIds'),true);
});

test('official and ECO options sort by last name then first name',()=>{
 const records=[
  {FirstName:'Zoe',LastName:'Adams'},
  {FirstName:'Amy',LastName:'Brown'},
  {FirstName:'Zara',LastName:'Brown'},
  {FirstName:'Aaron',LastName:'Adams'}
 ];
 const names=records.map(record=>`${record.FirstName} ${record.LastName}`);
 names.sort(officialNameComparator(records));
 assert.deepEqual(names,['Aaron Adams','Zoe Adams','Amy Brown','Zara Brown']);
});

test('1099 association amounts use the Grand Totals currency format',()=>{
 assert.equal(reportFieldValue('1099','amount','1000'),'$1,000.00');
 assert.equal(reportFieldValue('1099','amount','12.50'),'$12.50');
 assert.equal(reportFieldValue('1099','amount','0'),'$0.00');
});

test('All associations omits the association filter while explicit selections are repeated',()=>{
 const all=new URLSearchParams({taxYear:'2025'});
 appendAssociationParams(all,[]);
 const explicit=new URLSearchParams({taxYear:'2025'});
 appendAssociationParams(explicit,['MSO','GOLLC']);
 assert.equal(all.toString(),'taxYear=2025');
 assert.deepEqual(explicit.getAll('association'),['MSO','GOLLC']);
});
