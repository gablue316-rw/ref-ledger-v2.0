import test from 'node:test';
import assert from 'node:assert/strict';
import {gameURL,inputDate,inputTime,buildGame,responseError} from './game-data.js';
test('editor converts dates and midnight/noon without a timezone shift',()=>{
 assert.equal(inputDate('11/10/2026'),'2026-11-10');
 assert.equal(inputTime('12:05 AM'),'00:05');assert.equal(inputTime('12:05 PM'),'12:05');assert.equal(inputTime('7:30 PM'),'19:30');
 for(const [time,expected] of [['00:05','12:05 AM'],['12:05','12:05 PM'],['19:30','7:30 PM']])assert.equal(buildGame({}, {Date:'2026-11-10',Time:time},[]).Time,expected);
});
test('saving preserves identity and stored fees rather than dashboard net earnings',()=>{
 const original={Association:'MSO',GameId:'45',GameFee:'85.00',TravelPay:'10.00',NumOfGames:'2',Notes:'keep'};
 const officials=[{roleId:'plate',officialId:'9',name:'John Doe'}];
 const updated=buildGame(original,{Date:'2026-11-10',Time:'17:00',Field:'3',Status:'Completed'},officials);
 assert.equal(updated.GameFee,'85.00');assert.equal(updated.TravelPay,'10.00');assert.equal(updated.GameId,'45');assert.equal(updated.Notes,'keep');assert.deepEqual(updated.Officials,officials);assert.equal(original.Field,undefined);
});
test('composite identity is safely encoded',()=>{
 const u=new URL(gameURL('https://example.test',{Association:'A & B/League',GameId:42}));
 assert.equal(u.searchParams.get('association'),'A & B/League');assert.equal(u.searchParams.get('gameId'),'42');assert.equal(u.pathname,'/api/mobile/game');
});
test('maintenance and conflicts remain actionable',()=>{
 assert.match(responseError({status:503,data:{error:'Maintenance: changes disabled'}}),/Maintenance/);
 assert.match(responseError({status:409,data:'Game changed; reopen it'}),/reopen/);
});
