import {buildGame,inputDate,inputTime} from './game-data.js';
export const gameKey=game=>JSON.stringify([game.Association,String(game.GameId)]);
// An unchecked field must retain each assignment's own value, including fees.
export function applyBulkChanges(original,changes,officialChanges={}){
 const officials=(original.Officials||[]).map(o=>Object.hasOwn(officialChanges,o.roleId)?{...o,...officialChanges[o.roleId]}:{...o});
 for(const [roleId,assignment] of Object.entries(officialChanges))if(!officials.some(o=>o.roleId===roleId))officials.push({roleId,...assignment});
 return buildGame(original,{...changes,Date:changes.Date??inputDate(original.Date),Time:changes.Time??inputTime(original.Time)},officials);
}
// Each request retains the server's version check and current account permissions.
// A failed/uncertain request never counts as a success and never retries a delete.
export async function runBulkWrites(snapshots,method,makeGame,write,onProgress=()=>{}){
 const results=[];
 for(const snapshot of snapshots){
  const game=snapshot.game;
  onProgress(results.length,snapshots.length,game);
  try{
   const response=await write(game,method,{version:snapshot.version,...(method==='PUT'?{game:makeGame(game)}:{})});
   results.push({game,ok:response.status>=200&&response.status<300,response});
  }catch(error){results.push({game,ok:false,uncertain:true,error:'Could not confirm the result. Refresh and check this game before retrying.'});}
 }
 onProgress(results.length,snapshots.length);
 return results;
}
