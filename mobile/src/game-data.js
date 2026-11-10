export function gameURL(base,game){
 const url=new URL('/api/mobile/game',base);
 url.searchParams.set('association',game.Association);
 url.searchParams.set('gameId',game.GameId);
 return url.href;
}
export function inputDate(value){
 if(/^\d{4}-\d{2}-\d{2}$/.test(value))return value;
 const m=/^(\d{1,2})\/(\d{1,2})\/(\d{4})$/.exec(value);
 if(!m)throw new Error('Invalid game date.');
 return `${m[3]}-${m[1].padStart(2,'0')}-${m[2].padStart(2,'0')}`;
}
export function inputTime(value){
 const m=/^(\d{1,2}):(\d{2})\s*(AM|PM)$/i.exec(value);
 if(!m)throw new Error('Invalid game time.');
 return `${String(Number(m[1])%12+(m[3].toUpperCase()==='PM'?12:0)).padStart(2,'0')}:${m[2]}`;
}
export function buildGame(original,values,officials){
 const game={...original,...values,Officials:officials};
 const [year,month,day]=values.Date.split('-');
 game.Date=`${Number(month)}/${Number(day)}/${year}`;
 const [hour,minute]=values.Time.split(':').map(Number);
 game.Time=`${hour%12||12}:${String(minute).padStart(2,'0')} ${hour>=12?'PM':'AM'}`;
 return game;
}
export function responseError(r){
 if(r.status===401)return 'Your session expired. Sign out and sign in again.';
 if(r.status===403)return 'Your account does not have permission to make this change.';
 return typeof r.data==='string'?r.data.trim():r.data?.error||r.data?.message||'Unable to complete the request. Please retry.';
}

export const gameFields=[['Date','Date','date'],['Time','Time (Eastern)','time'],['Sport','Sport'],['Level','Level'],['Site','Site'],['Field','Field / court'],['Home','Home'],['Visitor','Visitor'],['NumOfGames','Number of games','number'],['GameFee','Game fee per game','number'],['TravelPay','Travel pay','number'],['AssignorFee','Assignor fee','number'],['Deductions','Deductions','number'],['Assignor','Assignor'],['TaxYear','Tax year','number']];
