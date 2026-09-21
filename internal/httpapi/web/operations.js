'use strict';
(() => {
 let loading=false;
 const receiverLabels={online:'接收正常',connecting:'连接中',disabled:'未启用',error:'接收异常',stale:'状态超时'};
 const senderLabels={idle:'暂无待发任务',working:'正在发送',delayed:'等待时间较长',blocked:'需要处理'};
 const businessLabels={idle:'尚未处理更新',healthy:'处理正常',error:'处理失败，重试中'};
 const ms=v=>Number(v||0).toFixed(2)+' ms';
 function healthCard(id,state,label,detail){const el=$(id);el.dataset.state=state;el.querySelector('strong').textContent=label;el.querySelector('p').textContent=detail;}
 async function loadOperations(){
  if(!token||loading)return;loading=true;const auth=token;
  $('ops-updated').textContent='正在更新运行状态…';
  try{
   const d=await api('operations',undefined,false,8000);if(token!==auth)return;
   const r=d.receiver,b=d.business,q=d.sender,db=d.database;
   healthCard('ops-receiver',r.receiver_state,receiverLabels[r.receiver_state]||'未知',r.updated_at?'最近接收状态 '+when(r.updated_at):'等待接收器报告状态');
   healthCard('ops-business',b.state,businessLabels[b.state]||'未知',b.failed_update?'失败更新编号 '+b.failed_update:'本次启动已处理 '+b.processed+' 次更新（含重复检查）');
   healthCard('ops-sender',q.state,senderLabels[q.state]||'未知',q.pending+' 条待发 · '+q.inflight+' 条发送中 · '+q.needs_review+' 条待处理');
   $('ops-oldest').textContent=q.oldest_age+' 秒';$('ops-oldest-help').textContent=q.oldest_age>=60?'等待超过60秒，请检查发送队列、限流和网络':'当前最老的待发或发送中任务';
   $('ops-db-wait').textContent=ms(db.operations?db.wait_ms/db.operations:0);$('ops-db-work').textContent=ms(db.operations?db.work_ms/db.operations:0);
   $('ops-diagnostics').innerHTML=table(['观测项','当前记录'],[['启动时间',esc(when(b.started_at))],['数据库外层操作次数',esc(db.operations)],['数据库最长锁等待',esc(ms(db.max_wait_ms))],['数据库最长操作（含事务）',esc(ms(db.max_work_ms))],['最近业务处理耗时',esc(ms(b.last_ms))],['最长业务处理耗时',esc(ms(b.max_ms))],['业务处理失败次数',esc(b.failures)],['最近业务成功',esc(b.last_success?when(b.last_success):'暂无')],['最近业务失败',esc(b.last_failure?when(b.last_failure):'暂无')]]);
   $('ops-updated').textContent='更新于 '+when(d.checked_at)+' · 本页每5秒自动更新';
  }catch(e){if(token!==auth)return;for(const id of ['ops-receiver','ops-business','ops-sender'])healthCard(id,'unknown','状态无法获取','请检查服务或网络，旧状态已清除');$('ops-updated').textContent=e.message;$('ops-diagnostics').textContent='监控读取失败，诊断数据暂不可用。';for(const id of ['ops-oldest','ops-db-wait','ops-db-work'])$(id).textContent='—';}
  finally{loading=false;}
 }
 $('ops-refresh').onclick=()=>loadOperations();
 const originalShow=showTab;showTab=async function(tab){await originalShow(tab);if(tab==='checks')await loadOperations();};
 setInterval(()=>{if(token&&activeTab==='checks'&&!document.hidden)void loadOperations();},5000);

 // Keep visited pages handy without rebuilding forms or losing their draft input.
 const visited=new Map(),nav=$('workspace-tabs');
 function renderTabs(){nav.innerHTML=[...visited].map(([tab,label])=>'<div class="workspace-tab"><button type="button" data-tab="'+esc(tab)+'"'+(activeTab===tab?' aria-current="page"':'')+'>'+esc(label)+'</button>'+(tab==='round'?'':'<button type="button" data-close-tab="'+esc(tab)+'" aria-label="关闭'+esc(label)+'标签">×</button>')+'</div>').join('');}
 const beforeTabs=showTab;showTab=async function(tab){await beforeTabs(tab);const label=document.querySelector('.sidebar [data-tab="'+tab+'"]')?.textContent.trim()||tab;visited.set(tab,label);renderTabs();};
 nav.addEventListener('click',e=>{const btn=e.target.closest('[data-close-tab]');if(!btn)return;const tab=btn.dataset.closeTab;visited.delete(tab);if(activeTab===tab)busy(btn,()=>showTab('round'));else renderTabs();});
 const toggle=$('nav-toggle');toggle.onclick=()=>{const collapsed=document.body.classList.toggle('nav-compact');toggle.setAttribute('aria-expanded',String(!collapsed));toggle.setAttribute('aria-label',collapsed?'展开侧边导航':'收起侧边导航');};
 document.querySelectorAll('.sidebar [data-tab]').forEach(btn=>btn.title=btn.textContent.trim());
})();
