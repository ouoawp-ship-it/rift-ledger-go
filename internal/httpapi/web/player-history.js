'use strict';
(() => {
 let selected='',view='bets',pages=[''],next='',loadSeq=0,searchSeq=0;
 const filters={bets:[['','全部结果'],['WIN','中奖'],['LOSS','未中奖'],['VOID','流局 / 已退回'],['RESERVED','待结算']],entries:[['','全部流水'],['CREDIT','上分'],['DEBIT','下分'],['GAME','游戏盈亏']],requests:[['','全部状态'],['PENDING','待审批'],['APPROVED','已批准'],['REJECTED','已拒绝']]};
 const outcomes={WIN:'中奖',LOSS:'未中奖',VOID:'流局 / 已退回',RESERVED:'待结算'};
 const badge=(value,label)=>'<span class="ph-badge" data-state="'+esc(value)+'">'+esc(label)+'</span>';
 const amount=v=>'<span class="ph-money '+(Number(v)>0?'ph-positive':Number(v)<0?'ph-negative':'')+'">'+esc(signed(v))+'</span>';
 function setView(value){view=value;pages=[''];next='';$('ph-filter').innerHTML=filters[view].map(([v,label])=>'<option value="'+v+'">'+label+'</option>').join('');document.querySelectorAll('[data-history-view]').forEach(b=>b.setAttribute('aria-pressed',String(b.dataset.historyView===view)));$('ph-date-help').textContent=(view==='bets'?'按下注时间筛选':view==='entries'?'按入账时间筛选':'按申请时间筛选')+' · 北京时间，包含开始和结束当天';}
 async function search(){
  const seq=++searchSeq,auth=token;$('ph-search-result').textContent='正在查找玩家…';
  try{const d=await api('player-search?q='+encodeURIComponent($('ph-search').value.trim()));if(seq!==searchSeq||token!==auth)return;
   $('ph-search-result').innerHTML=d.rows.length?'<div class="ph-matches">'+d.rows.map(a=>'<button type="button" data-player-history="'+esc(a.id)+'"><strong>'+esc(a.name)+'</strong><span>'+esc(a.username?'@'+a.username:'未设置用户名')+' · TG '+esc(a.telegram_id)+'</span></button>').join('')+'</div>'+(d.has_more?'<p class="hint">结果超过20人，请输入更完整的昵称、用户名或TG ID。</p>':''):'<p class="hint">没有找到匹配的玩家。</p>';
  }catch(e){if(seq===searchSeq)$('ph-search-result').textContent=e.message;}
 }
 function rowsHTML(rows){
  if(!rows.length)return '<div class="ph-empty"><strong>暂无符合条件的记录</strong><p>可以调整日期或筛选条件后重试。</p></div>';
  if(view==='bets')return table(['下注时间 / 期号','英雄','下注积分','开奖结果','实际盈亏','结算时间'],rows.map(b=>[esc(when(b.created_at))+'<small>'+esc(b.round_number)+'期</small>',esc(b.position)+'号 · '+esc(b.hero),'<span class="ph-money">'+esc(fmt(b.stake))+'</span>',badge(b.state,outcomes[b.state]||b.state),b.state==='RESERVED'?'—':amount(b.net_delta),esc(when(b.settled_at))]));
  if(view==='entries')return table(['入账时间','类型 / 来源','积分变化','变动后余额','期号','备注'],rows.map(e=>{const label=e.kind==='ADJUST'?(Number(e.delta)>0?'上分':Number(e.delta)<0?'下分':'调分'):(kindLabel[e.kind]||({FEE:'历史费用',FEE_REFUND:'历史费用退回'}[e.kind])||e.kind);return [esc(when(e.created_at)),esc(label)+(e.kind==='ADJUST'?'<small>'+(e.adjustment_source==='REQUEST'?'玩家申请获批':'后台人工操作')+'</small>':''),amount(e.delta),'<span class="ph-money">'+esc(fmt(e.balance_after))+'</span>',esc(e.round_number?e.round_number+'期':'—'),esc(e.note||'—')];}));
  return table(['申请时间','类型 / 金额','状态','处理时间','申请时余额','审批备注'],rows.map(r=>[esc(when(r.created_at)),(r.kind==='CREDIT'?'上分':'下分')+'<small class="ph-money">'+esc(fmt(r.amount))+'</small>',badge(r.state,requestStates[r.state]||r.state),esc(when(r.processed_at)),'<span class="ph-money">'+esc(fmt(r.balance_at_request))+'</span>',esc(r.note||'—')]));
 }
 async function load(stack=['']){
  if(!selected)return;
  const seq=++loadSeq,auth=token;
  $('ph-detail').hidden=false;$('ph-results').setAttribute('aria-busy','true');$('ph-results').textContent='正在加载记录…';$('ph-prev').disabled=true;$('ph-next').disabled=true;
  const params=new URLSearchParams({account_id:selected,view,filter:$('ph-filter').value,from:$('ph-from').value,to:$('ph-to').value,cursor:stack.at(-1),limit:'25'});
  try{
   const d=await api('player-history?'+params);if(seq!==loadSeq||auth!==token)return;
   pages=stack;next=d.next_cursor;const a=d.account;
   $('ph-player-name').textContent=a.name;$('ph-player-identity').textContent=(a.username?'@'+a.username+' · ':'')+'TG ID '+a.telegram_id+' · '+(a.enabled?'已开通':'未开通 / 已停用');
   $('ph-balance').textContent=fmt(a.balance);$('ph-available').textContent=fmt(a.available);$('ph-locked').textContent=fmt(a.locked);
   $('ph-results').innerHTML=rowsHTML(d.rows||[]);const headers=[...$('ph-results').querySelectorAll('th')].map(th=>th.textContent);$('ph-results').querySelectorAll('tbody tr').forEach(tr=>[...tr.children].forEach((td,i)=>td.dataset.label=headers[i]));$('ph-page').textContent='第 '+pages.length+' 页 · 每页25条'+(next?'':' · 已到最后一页');
   $('ph-prev').disabled=pages.length===1;$('ph-next').disabled=!next;
  }catch(e){if(seq===loadSeq&&auth===token){$('ph-results').textContent='加载失败：'+e.message;$('ph-page').textContent='请点击“查询”重试';}}
  finally{if(seq===loadSeq)$('ph-results').setAttribute('aria-busy','false');}
 }
 async function open(id){
  selected=id;++searchSeq;++loadSeq;$('ph-search-result').replaceChildren();$('ph-player-name').textContent='正在读取玩家…';$('ph-player-identity').textContent='';
  for(const id of ['ph-balance','ph-available','ph-locked'])$(id).textContent='—';
  $('ph-from').value='';$('ph-to').value='';setView('bets');await showTab('player-history');$('ph-player-name').focus();
 }
 $('ph-search-form').addEventListener('submit',e=>{e.preventDefault();void search();});
 $('ph-filter-form').addEventListener('submit',e=>{e.preventDefault();void load();});
 for(const id of ['ph-from','ph-to','ph-filter'])$(id).addEventListener('change',()=>{++loadSeq;$('ph-prev').disabled=true;$('ph-next').disabled=true;$('ph-results').setAttribute('aria-busy','false');$('ph-results').textContent='筛选条件已修改，请点击“查询”。';$('ph-page').textContent='';});
 $('ph-reset').addEventListener('click',()=>{$('ph-from').value='';$('ph-to').value='';$('ph-filter').value='';void load();});
 $('ph-prev').addEventListener('click',()=>{if(pages.length>1)void load(pages.slice(0,-1));});
 $('ph-next').addEventListener('click',()=>{if(next)void load([...pages,next]);});
 document.addEventListener('click',e=>{const b=e.target.closest('button');if(!b||b.disabled)return;if(b.dataset.playerHistory)busy(b,()=>open(b.dataset.playerHistory));if(b.dataset.historyView){setView(b.dataset.historyView);void load();}if(b.id==='logout'){++loadSeq;++searchSeq;selected='';$('ph-detail').hidden=true;$('ph-search-result').replaceChildren();$('ph-results').replaceChildren();}});
 const beforeShow=showTab;showTab=async function(tab){await beforeShow(tab);if(tab==='player-history'){if(selected)await load();else await search();}};
 setView('bets');
})();
