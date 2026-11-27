import test from 'node:test';
import assert from 'node:assert/strict';
import {announcementURL,startAnnouncementLoop} from './announcements.js';

test('announcement URL carries the known key on the mobile endpoint',()=>{
 assert.equal(announcementURL('https://x.test','a:1'),'https://x.test/api/mobile/announcements/current?known=a%3A1');
 assert.equal(announcementURL('https://x.test'),'https://x.test/api/mobile/announcements/current?known=');
});

test('loop applies changes without restart, clears, and stops on ended session',async()=>{
 const seen=[],known=[];
 const replies=[
  {status:200,data:{announcement:{message:'hi'},key:'a:1'}},
  {status:200,data:{announcement:null,key:''}},
  {status:500},
  {status:200,data:{announcement:{message:'again'},key:'b:1'}},
  {status:401}
 ];
 const done=new Promise(resolve=>{
  startAnnouncementLoop({
   request:async k=>{known.push(k);const r=replies.shift();if(!replies.length)setTimeout(resolve,0);return r;},
   apply:a=>seen.push(a?.message??null),
   wait:async()=>{}
  });
 });
 await done;await new Promise(r=>setTimeout(r,5));
 assert.deepEqual(seen,['hi',null,'again',null]);
 assert.deepEqual(known,['','a:1','','','b:1']);
});

test('stop clears the banner and halts polling',async()=>{
 let calls=0;const seen=[];
 const loop=startAnnouncementLoop({request:async()=>{calls++;await new Promise(r=>setTimeout(r,5));return {status:200,data:{announcement:{message:'x'},key:'k'}};},apply:a=>seen.push(a?.message??null),wait:async()=>{}});
 loop.stop();
 await new Promise(r=>setTimeout(r,30));
 assert.deepEqual(seen,[null]);
 assert.equal(calls,1);
});
