import test from 'node:test';
import assert from 'node:assert/strict';
import {applyBulkChanges,gameKey,runBulkWrites} from './game-bulk-data.js';
const game=(id,extra={})=>({GameId:String(id),Association:'MSO',Date:'11/10/2026',Time:'7:30 PM',GameFee:'85.00',TravelPay:'10.00',Notes:'Keep',Officials:[{roleId:'plate',officialId:'9',name:'John Doe'},{roleId:'u1',officialId:'11',name:'Other Official'}],...extra});
test('bulk updates only checked fields and preserves each game fee and official',()=>{
 for(const original of [game(1),game(2,{GameFee:'95.00',Date:'11/11/2026'})]){
  const updated=applyBulkChanges(original,{Field:'4',Status:'Completed'});
  assert.equal(updated.Field,'4');assert.equal(updated.GameFee,original.GameFee);assert.equal(updated.Date,original.Date);assert.equal(updated.Time,original.Time);assert.equal(updated.Notes,'Keep');assert.deepEqual(updated.Officials,original.Officials);assert.equal(original.Field,undefined);
 }
});
test('bulk explicit blank clears notes and one official while retaining other role',()=>{
 const updated=applyBulkChanges(game(1),{Notes:'',Time:'00:15'},{plate:{officialId:'',name:'Unassigned'}});
 assert.equal(updated.Notes,'');assert.equal(updated.Time,'12:15 AM');assert.equal(updated.Officials[0].officialId,'');assert.equal(updated.Officials[1].officialId,'11');
});
test('bulk identities distinguish associations and normalize numeric IDs',()=>{
 assert.notEqual(gameKey(game(1)),gameKey(game(1,{Association:'Other'})));assert.equal(gameKey(game(1)),gameKey(game(1,{GameId:1})));
});
test('bulk reports partial success, conflict, and uncertain write without retrying',async()=>{
 const calls=[],snapshots=[1,2,3].map(id=>({game:game(id),version:`version-${id}`}));
 const results=await runBulkWrites(snapshots,'DELETE',()=>{throw Error('not called for deletes')},async(g,method,data)=>{
  calls.push({g,method,data});if(g.GameId==='3')throw Error('timeout');return {status:g.GameId==='2'?409:204,data:'Game changed; reopen it'};
 });
 assert.equal(calls.length,3);assert.deepEqual(calls.map(c=>c.data),[{version:'version-1'},{version:'version-2'},{version:'version-3'}]);assert.deepEqual(results.map(r=>r.ok),[true,false,false]);assert.equal(results[2].uncertain,true);
});
test('bulk update sends per-game versions and preserves separate amounts',async()=>{
 const snapshots=[{game:game(1),version:'one'},{game:game(2,{GameFee:'120.00'}),version:'two'}],sent=[];
 await runBulkWrites(snapshots,'PUT',g=>applyBulkChanges(g,{Field:'7'}),async(g,m,data)=>{sent.push(data);return {status:204}});
 assert.deepEqual(sent.map(d=>[d.version,d.game.GameFee,d.game.Field]),[['one','85.00','7'],['two','120.00','7']]);
});
