'use strict';
let botConnectionTimer=null,botConnectionRequest=null,botConnectionReceivedAt=0;
const botConnectionLabels={online:'机器人接收正常',degraded:'机器人运行需处理',connecting:'机器人连接中',disabled:'机器人未启用',error:'机器人连接异常',stale:'机器人连接超时',unreachable:'机器人状态无法获取',unknown:'机器人状态待连接'};

function renderBotConnection(status){
 const code=Object.hasOwn(botConnectionLabels,status.state)?status.state:'unknown';
 const badge=$('bot-connection');
 badge.dataset.state=code;
 badge.title=status.message;
 $('bot-connection-label').textContent=botConnectionLabels[code];
 const known=Number.isInteger(status.pending)&&Number.isInteger(status.needs_review);
 $('bot-queue-count').textContent=known?status.pending+' 条待发送'+(status.sender?.inflight?' · '+status.sender.inflight+' 条发送中':''):'—';
 $('bot-queue-count').dataset.warning=String(known&&status.needs_review>0);
 $('bot-queue-detail').textContent=known?(status.needs_review?status.needs_review+' 条失败或待核实，请在发送记录处理':(status.sender?.oldest_age>=60?'最老任务等待 '+status.sender.oldest_age+' 秒，请检查发送队列':'暂无失败或待核实消息')):'暂时无法确认发送状态';
 $('bot-connection-panel').dataset.state=code;
 $('bot-panel-label').textContent=botConnectionLabels[code].replace('机器人','');
 $('bot-connection-detail').textContent=status.message;
 $('bot-connection-time').textContent=(status.updated_at?'最近接收状态更新：'+when(status.updated_at)+' · ':'')+'每5秒自动检测';
}

async function pollBotConnection(){
 if(!token||botConnectionRequest)return;
 const controller=new AbortController();
 botConnectionRequest=controller;
 const timeout=setTimeout(()=>controller.abort(),8000);
 try{
  const response=await fetch('/api/bot-connection',{headers:{Authorization:'Bearer '+token},cache:'no-store',signal:controller.signal});
  const result=await response.json();
  if(!response.ok||!result.ok||!result.data)throw new Error('状态获取失败');
  if(botConnectionRequest!==controller||!token)return;
  botConnectionReceivedAt=Date.now();
  renderBotConnection(result.data);
 }catch{
  if(botConnectionRequest===controller&&token)renderBotConnection({state:'unreachable',message:'无法获取机器人状态，请检查后台服务或网络连接。'});
 }finally{
  clearTimeout(timeout);
  if(botConnectionRequest===controller)botConnectionRequest=null;
 }
}

function stopBotConnectionWatcher(){
 clearInterval(botConnectionTimer);
 botConnectionTimer=null;
 if(botConnectionRequest)botConnectionRequest.abort();
 botConnectionRequest=null;
 botConnectionReceivedAt=0;
}

function startBotConnectionWatcher(){
 stopBotConnectionWatcher();
 renderBotConnection({state:'connecting',message:'正在获取机器人实时连接状态…'});
 void pollBotConnection();
 botConnectionTimer=setInterval(()=>{
  if(botConnectionReceivedAt&&Date.now()-botConnectionReceivedAt>15000)renderBotConnection({state:'unreachable',message:'机器人状态未能及时更新，正在重新检测。'});
  void pollBotConnection();
 },5000);
}

// A suspended/background tab must revalidate instead of displaying an old green light.
document.addEventListener('visibilitychange',()=>{if(!document.hidden&&token)startBotConnectionWatcher();});
window.addEventListener('offline',()=>{
 if(!token)return;
 stopBotConnectionWatcher();
 renderBotConnection({state:'unreachable',message:'浏览器网络已断开，暂时无法确认机器人连接状态。'});
});
window.addEventListener('online',()=>{if(token)startBotConnectionWatcher();});
