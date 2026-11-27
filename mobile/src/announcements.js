// Announcement banner for signed-in screens. Message text is rendered with
// textContent only. The server owns all state; this loop long-polls it.
export const announcementPath='/api/mobile/announcements/current';

export function announcementURL(base,known){
 const url=new URL(announcementPath,base);
 url.searchParams.set('known',known??'');
 return url.toString();
}

export function isOverflowing(el){
 return el.scrollWidth>el.clientWidth||el.textContent.includes('\n');
}

// Runs until stop(). request(known) resolves to an HTTP-like {status,data}.
// 401 means the session ended (the auth layer handles sign-out), so the loop stops.
export function startAnnouncementLoop({request,apply,wait=ms=>new Promise(r=>setTimeout(r,ms)),retryMs=5000}){
 let running=true,known='';
 (async()=>{
  while(running){
   let response;
   try{response=await request(known);}catch{response=undefined;}
   if(!running)return;
   if(response?.status===200&&response.data&&typeof response.data.key==='string'){
    known=response.data.key;
    apply(response.data.announcement||null);
   }else if(response?.status===401){
    apply(null);
    running=false;
   }else{
    await wait(retryMs);
   }
  }
 })();
 return {stop(){running=false;apply(null);}};
}

export function createAnnouncementBanner({banner,text,more,dialog,body,close,watch,doc=document}){
 let loop;
 const render=a=>{
  if(!a||!a.message){
   if(dialog.open)dialog.close();
   banner.hidden=true;text.textContent='';more.hidden=true;body.textContent='';
   return;
  }
  text.textContent=a.message;banner.hidden=false;
  more.hidden=!isOverflowing(text);
  if(dialog.open)body.textContent=a.message;
 };
 more.onclick=()=>{body.textContent=text.textContent;if(!dialog.open)dialog.showModal();};
 close.onclick=()=>dialog.close();
 const stop=()=>{loop?.stop();loop=undefined;};
 const start=()=>{if(loop||doc.visibilityState==='hidden')return;loop=watch(render);};
 // Foreground only: reconcile from scratch on resume.
 doc.addEventListener('visibilitychange',()=>{if(doc.visibilityState==='hidden'){stop();}else if(wanted)start();});
 let wanted=false;
 return {
  start(){wanted=true;start();},
  stop(){wanted=false;stop();render(null);},
  render
 };
}
