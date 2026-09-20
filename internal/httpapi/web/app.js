'use strict';
const $=id=>document.getElementById(id);
const esc=v=>String(v??'').replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
const fmt=v=>Number(v??0).toLocaleString('zh-CN');
const signed=v=>(Number(v)>0?'+':'')+fmt(v);
const when=v=>Number(v)?new Intl.DateTimeFormat('zh-CN',{timeZone:'Asia/Shanghai',year:'numeric',month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',second:'2-digit',hour12:false}).format(new Date(Number(v)*1000)):'—';
const states={DRAFT:'待配置',OPEN:'受理中',CLOSED:'已封盘',SETTLED:'已结算',VOID:'流局',RESERVED:'待结算',WIN:'赢',LOSS:'输',BANKER:'庄家',PENDING:'待发送',INFLIGHT:'发送中',SENT:'已处理',FAILED:'发送失败',UNKNOWN:'结果未知'};
const kindLabel={GAME:'游戏盈亏',ADJUST:'人工调分'};
let token='',state=null,activeTab='round',formRoundID=null,previewData=null,rulesVersion=0,rulesLoaded=false,stateSeq=0;
let championCatalog=[];
const championAliases={ys:'Yasuo',gl:'Garen',dl:'Darius',jz:'Jinx',lq:'LeeSin',ez:'Ezreal',akl:'Akali',lb:'LeBlanc',vn:'Vayne',ky:'Kayle',瑞文:'Riven',亚索:'Yasuo',盖伦:'Garen'};
let offsets={players:0,history:0,entries:0,messages:0};
const requestStates={PENDING:'待处理',APPROVED:'已批准',REJECTED:'已拒绝'};
const pending=new Map();
function requestID(){if(crypto.randomUUID)return crypto.randomUUID();return Array.from(crypto.getRandomValues(new Uint8Array(20)),x=>x.toString(16).padStart(2,'0')).join('');}
function notice(text,error=false){$('notice').textContent=text;$('notice').className=error?'error':'success';}
async function api(path,body,mutate=false){
 const signature=path+'\n'+JSON.stringify(body),id=mutate?(pending.get(signature)||requestID()):null;
 if(id)pending.set(signature,id);
 const options={method:body===undefined?'GET':'POST',headers:{Authorization:'Bearer '+token},cache:'no-store'};
 if(body!==undefined){options.headers['Content-Type']='application/json';options.body=JSON.stringify(body);}
 if(id)options.headers['Idempotency-Key']=id;
 let response;
 for(let attempt=0;attempt<2;attempt++){
  try{response=await fetch('/api/'+path,options);break;}catch(e){if(attempt===1)throw new Error('连接中断，操作结果未知。请先刷新核实；再次提交相同内容会复用业务号，避免重复记账。');}
 }
 let result;try{result=await response.json();}catch(e){throw new Error('服务器响应不完整，结果未知；请保持请求内容不变并刷新核实。');}
 // A 500 can occur after an ambiguous storage/network outcome: retain its key.
 if(id&&response.status<500)pending.delete(signature);
 if(!response.ok||!result.ok)throw new Error(result.error||'服务请求失败');
 return result.data;
}
async function busy(btn,fn){if(btn)btn.disabled=true;try{await fn();}catch(e){notice(e.message,true);}finally{if(btn)btn.disabled=false;controls();}}
function bind(id,fn){$(id).addEventListener('click',e=>{e.preventDefault();busy(e.currentTarget,fn);});}
function bindForm(id,fn){$(id).addEventListener('submit',e=>{e.preventDefault();busy(e.submitter,fn);});}
function table(headers,rows){if(!rows.length)return '<p class="hint">暂无记录。</p>';return '<table><thead><tr>'+headers.map(x=>'<th>'+esc(x)+'</th>').join('')+'</tr></thead><tbody>'+rows.map(row=>'<tr>'+row.map(v=>'<td>'+v+'</td>').join('')+'</tr>').join('')+'</tbody></table>';}
function tdnum(v){return '<span class="mono">'+esc(fmt(v))+'</span>';}
function getID(){if(!state?.active_round)throw new Error('当前没有活动期次');return state.active_round.id;}
function invalidatePreview(){previewData=null;$('settle').disabled=true;$('preview-result').innerHTML='<p class="hint">输入已改变，请重新生成预览。</p>';}
function controls(){
 if(!state)return;const r=state.active_round,st=r?.state;
 $('create-round').disabled=!!r;$('save-heroes').disabled=st!=='DRAFT';$('open-round').disabled=st!=='DRAFT';$('close-round').disabled=st!=='OPEN';$('preview').disabled=st!=='CLOSED';$('settle').disabled=st!=='CLOSED'||!previewData;
 $('number').disabled=!!r&&st!=='DRAFT';
 document.querySelectorAll('#hero-inputs input').forEach(x=>x.disabled=st!=='DRAFT');
 document.querySelectorAll('#damage-inputs input').forEach(x=>x.disabled=st!=='CLOSED');
}
async function refreshState(force=false){
 const seq=++stateSeq;const next=await api('state');if(seq!==stateSeq)return;state=next;
 $('connection').textContent='已连接 · v'+state.version;$('runtime').innerHTML=table(['项目','当前值'],[['服务版本',esc(state.version)],['SQLite',esc(state.sqlite_version)],['Telegram',esc(state.bot_status)],['公示群／话题',esc(state.group_id+' / '+state.topic_id)],['未处理发送任务',esc(state.outbox_unsent)],['服务时间（北京时间）',esc(when(state.server_time))]]);
 const r=state.active_round;$('round-state').textContent=r?r.number+'期 · '+states[r.state]:'尚无期次';$('round-version').textContent=r?'本期规则版本 '+r.rules_version+'｜修订 '+r.revision:'';
 if((r?.id||'')!==formRoundID||force){
  formRoundID=r?.id||'';$('number').value=r?.number||state.suggested_number;
  for(let i=1;i<=5;i++){$('hero-id-'+i).value=r?.heroes?.[i-1]?.id||'';$('hero-search-'+i).value=r?.heroes?.[i-1]?.name||'';$('hero-name-'+i).value=r?.heroes?.[i-1]?.name||'';setChampionAvatar(i,r?.heroes?.[i-1]?.id);$('banker-'+i).checked=r?.banker===i;$('damage-'+i).value='';$('damage-result-'+i).textContent='等待输入';}
  previewData=null;$('preview-result').innerHTML='<p class="hint">封盘后填写五个原始伤害，先预览再确认。</p>';
 }
 $('round-bets').innerHTML=table(['时间','玩家账户','位置','本金','状态','注单ID'],state.bets.map(b=>[esc(when(b.created_at)),esc(b.account_id),esc(b.position),tdnum(b.stake),esc(states[b.state]),'<code>'+esc(b.id)+'</code>']));
 if(!rulesLoaded){renderRules();rulesLoaded=true;}if(!championCatalog.length){try{const d=await api('champions');championCatalog=d.champions||[]}catch{}}controls();
}
function renderRules(){const r=state.rules;rulesVersion=state.rules_version;$('rules-version').textContent='后端模板版本：'+rulesVersion;
 r.payout.forEach((v,i)=>$('odd-'+i).value=v);$('min-stake').value=r.min_stake;$('max-stake').value=r.max_stake;
 $('zero-triple').value=String(r.zero_triple);
}
async function showTab(tab){activeTab=tab;document.querySelectorAll('.page').forEach(p=>p.hidden=p.id!=='page-'+tab);document.querySelectorAll('[data-tab]').forEach(b=>b.classList.toggle('active',b.dataset.tab===tab));
 if(tab==='players')await loadPlayers();if(tab==='history'){await loadHistory();await loadEntries();}if(tab==='messages')await loadMessages();if(tab==='checks')await loadAudit();
}
function positionTable(p){return table(['位置','英雄／身份','原始伤害','规范化数字','牛型','最大数字','对庄结果','计算说明'],p.positions.map(x=>[esc(x.position),esc(x.hero.name+(x.banker?' [庄]':'')),esc(x.hand.raw),esc(x.hand.normalized||'—'),esc(x.hand.label),esc(x.hand.max_digit),esc(states[x.outcome]),esc(x.reason+'；'+x.hand.explanation)]));}
function damageLabel(raw){
 raw=String(raw||'').trim();
 if(!raw)return '等待输入';
 if(!/^[0-9]+$/.test(raw))return '请输入数字';
 if(raw.length<3||raw.length>6)return '需输入3至6位';
 if(raw[0]==='0')return '不能前导零';
 if(raw.length===3)return '流局';
 const normalized=(raw.length===4?'1'+raw:raw.length===6?raw.slice(1):raw),digits=[...normalized].map(Number),sum=digits.reduce((a,b)=>a+b,0);
 for(let i=0;i<3;i++)for(let j=i+1;j<4;j++)for(let k=j+1;k<5;k++){
  const triple=digits[i]+digits[j]+digits[k];
  if(triple%10!==0||(triple===0&&!state?.rules?.zero_triple))continue;
  const rank=(sum-triple)%10;
  return rank===0?'牛牛':'牛'+rank;
 }
 return '没牛';
}
function renderPreview(p){return '<div class="summary"><strong>'+esc(p.number)+'期</strong>｜'+(p.whole_void?'整期流局：'+esc(p.reason):'正常结算')+'<br>玩家游戏合计 '+signed(p.player_game_delta)+'；注单 '+p.lines.length+'笔</div><div class="table-wrap">'+positionTable(p)+'</div><h4>逐笔结算'+(p.lines.length>200?'（仅展示前200笔，汇总涵盖全部）':'')+'</h4><div class="table-wrap">'+table(['玩家账户','位置','本金','结果','游戏变化','全生命周期变化'],p.lines.slice(0,200).map(l=>[esc(l.account_id),esc(l.position),tdnum(l.stake),esc(states[l.outcome]),esc(signed(l.game_delta)),esc(signed(l.net_delta))]))+'</div>';}
function settleInput(){return {damages:Array.from({length:5},(_,i)=>$('damage-'+(i+1)).value.trim())};}
async function loadPlayers(){const page=offsets.players,d=await api('accounts?limit=50&offset='+page),rows=d.rows||d,stats=d.stats||{};$('players-page').textContent='第'+(page/50+1)+'页（每页最多50条）';$('players-table').innerHTML='<p class="summary">玩家数量：'+fmt(stats.players||0)+'｜玩家总分：'+fmt(stats.balance||0)+'｜今日盈亏：'+signed(stats.profit||0)+'</p>'+table(['玩家TG ID','玩家昵称','玩家TG用户名','分数','本期下注金额','下注内容','上期盈亏','操作'],rows.map(a=>[esc(a.telegram_id),esc(a.name),esc(a.username?'@'+a.username:'未设置'),tdnum(a.balance),tdnum(a.round_stake||0),esc(a.round_content||'—'),signed(a.last_profit||0),'<button data-player="'+esc(a.telegram_id)+'" data-name="'+esc(a.name)+'" data-enabled="'+a.enabled+'">编辑资格</button> <button data-account="'+esc(a.id)+'">调分</button>']));}
async function loadHistory(){const page=offsets.history,rows=await api('rounds?limit=10&offset='+page);$('history-page').textContent='第'+(page/10+1)+'页';$('history-table').innerHTML=table(['期号','状态','庄位','规则版本','结算时间','详情'],rows.map(r=>[esc(r.number),esc(states[r.state]),esc(r.banker||'未选'),esc(r.rules_version),esc(when(r.settled_at)),'<button data-round="'+esc(r.id)+'">查看</button>']));}
async function loadEntries(){const page=offsets.entries,id=$('entry-account').value.trim(),rows=await api('entries?limit=50&offset='+page+'&account_id='+encodeURIComponent(id));$('entries-page').textContent='第'+(page/50+1)+'页';$('entries-table').innerHTML=table(['时间','账户','类型','变化','变化后余额','备注','业务批次'],rows.map(e=>[esc(when(e.created_at)),esc(e.account_id),esc(kindLabel[e.kind]||e.kind),esc(signed(e.delta)),tdnum(e.balance_after),esc(e.note),'<code>'+esc(e.batch)+'</code>']));}
async function loadMessages(){const page=offsets.messages,rows=await api('outbox?limit=50&offset='+page);$('messages-page').textContent='第'+(page/50+1)+'页';$('messages-table').innerHTML=table(['ID／时间','会话','状态','次数','文本／错误','操作'],rows.map(m=>{let text='';try{text=JSON.parse(m.payload).text;}catch{};const actions=['FAILED','UNKNOWN'].includes(m.state)?'<div class="actions"><button data-message="'+m.id+'" data-resolve="retry">明确重试</button><button data-message="'+m.id+'" data-resolve="ack">核实已送达</button>'+(!m.card_key?'<button data-message="'+m.id+'" data-resolve="skip">跳过</button>':'')+'</div>':'—';return [esc(m.id)+'<br>'+esc(when(m.created_at)),esc(m.chat_id),esc(states[m.state]),esc(m.attempts),'<div class="detail">'+esc(text)+'\n'+esc(m.last_error)+'</div>',actions];}));}
async function loadAudit(){const rows=await api('audit?limit=50');$('audit-table').innerHTML=table(['时间','操作人','动作','业务号','内容'],rows.map(a=>[esc(when(a.created_at)),esc(a.actor),esc(a.action),'<code>'+esc(a.request_key)+'</code>','<div class="detail">'+esc(a.detail)+'</div>']));}
function nonempty(v,msg){if(!v)throw new Error(msg);return v;}

function setChampionAvatar(n,id){const c=championCatalog.find(x=>x.id===id);const img=$('hero-avatar-'+n);if(!img)return;if(c&&c.image_url){img.src=c.image_url;img.hidden=false}else{img.removeAttribute('src');img.hidden=true}}
function normalizeChampionText(value){return String(value??'').normalize('NFKC').trim().toLocaleLowerCase('zh-CN')}
function championMatches(query){
 query=normalizeChampionText(query);if(!query)return[];
 const alias=normalizeChampionText(championAliases[query]||query);
 return championCatalog
  .filter(c=>[c.name,c.title,c.english_name,c.id,c.key].some(v=>normalizeChampionText(v).includes(alias)))
  .sort((a,b)=>{
   const aName=normalizeChampionText(a.name),bName=normalizeChampionText(b.name);
   const aStarts=aName.startsWith(query)?0:aName.includes(query)?1:2;
   const bStarts=bName.startsWith(query)?0:bName.includes(query)?1:2;
   return aStarts-bStarts||aName.localeCompare(bName,'zh-CN');
  });
}
function chooseChampion(n,c){$('hero-id-'+n).value=c.id;$('hero-search-'+n).value=c.name+'（'+c.english_name+'）';$('hero-name-'+n).value=c.name;setChampionAvatar(n,c.id);$('hero-results-'+n).replaceChildren()}
function showChampionResults(n){const box=$('hero-results-'+n);box.replaceChildren();for(const c of championMatches($('hero-search-'+n).value)){const b=document.createElement('button');b.type='button';b.className='champion-option';b.innerHTML='<img src="'+esc(c.image_url)+'" alt=""><span>'+esc(c.name)+'<small>'+(c.title?esc(c.title)+' · ':'')+esc(c.english_name)+' · '+esc(c.id)+'</small></span>';b.addEventListener('click',()=>chooseChampion(n,c));box.appendChild(b)}}
$('hero-inputs').innerHTML=Array.from({length:5},(_,i)=>{const n=i+1;return '<tr><td>'+n+'号</td><td class="champion-picker"><input type="hidden" id="hero-id-'+n+'"><input type="search" id="hero-search-'+n+'" autocomplete="off" maxlength="40" placeholder="输入亚、Yasuo或ys"><div class="champion-results" id="hero-results-'+n+'"></div></td><td><input type="text" id="hero-name-'+n+'" readonly placeholder="从搜索结果选择"><img class="champion-avatar" id="hero-avatar-'+n+'" alt="已选英雄头像" hidden></td><td><label><input type="radio" name="banker" id="banker-'+n+'" value="'+n+'">选为庄家</label></td></tr>';}).join('');
for(let n=1;n<=5;n++){$('hero-search-'+n).addEventListener('input',()=>{$('hero-id-'+n).value='';$('hero-name-'+n).value='';setChampionAvatar(n,'');showChampionResults(n)});$('hero-search-'+n).addEventListener('focus',()=>showChampionResults(n));}
$('damage-inputs').innerHTML=Array.from({length:5},(_,i)=>'<label>'+(i+1)+'号原始伤害<input id="damage-'+(i+1)+'" inputmode="numeric" maxlength="6" placeholder="不要人为补零"><span class="damage-result" id="damage-result-'+(i+1)+'">等待输入</span></label>').join('');
$('odds-inputs').innerHTML=Array.from({length:11},(_,i)=>'<tr><td>'+(i===0?'没牛':i===10?'牛牛':'牛'+i)+'</td><td><input id="odd-'+i+'" type="number" min="0" max="100" step="0.01" required></td></tr>').join('');
document.querySelectorAll('#damage-inputs input').forEach((x,i)=>x.addEventListener('input',()=>{invalidatePreview();$('damage-result-'+(i+1)).textContent=damageLabel(x.value)}));
bindForm('login-form',async()=>{token=$('token').value.trim();try{await refreshState(true);}catch(e){token='';throw e;}$('token').value='';$('login-box').hidden=true;$('workspace').hidden=false;await showTab('round');notice('已连接。首次运行请先刷新英雄数据、确认赔率并开通玩家。');});
bind('logout',async()=>{token='';state=null;previewData=null;pending.clear();location.reload();});
bind('refresh',async()=>{await refreshState();await showTab(activeTab);notice('已刷新后端数据；尚未保存的规则和同一期草稿输入未被覆盖。');});
bind('create-round',async()=>{await api('rounds',{number:$('number').value.trim(),banker:0,heroes:[]},true);await refreshState();notice('首期草稿已创建，请填写敌方英雄并手动选庄。');});
bind('save-heroes',async()=>{const selected=document.querySelector('input[name=banker]:checked');if(!selected)throw new Error('请手动选择本期庄家英雄');const data={number:$('number').value.trim(),banker:Number(selected.value),heroes:Array.from({length:5},(_,i)=>({id:$('hero-id-'+(i+1)).value.trim(),name:$('hero-name-'+(i+1)).value.trim()}))};await api('rounds/'+getID()+'/configure',data,true);await refreshState();notice('英雄与庄家已保存，尚未开放下注。');});
bind('open-round',async()=>{if(!confirm('使用已保存的英雄、庄家和当前规则开始受理？尚未保存的输入不会生效。'))return;await api('rounds/'+getID()+'/open',{},true);await refreshState();notice('本期已经开始，英雄和规则快照已锁定。');});
bind('close-round',async()=>{if(!confirm('立即封盘？封盘后不再接受下注，查询仍可用。'))return;await api('rounds/'+getID()+'/close',{},true);await refreshState();notice('已经封盘，可以录入实际伤害并预览。');});
bind('preview',async()=>{previewData=await api('rounds/'+getID()+'/preview',settleInput());$('preview-result').innerHTML=renderPreview(previewData);notice('预览已生成，尚未改动余额。请逐项核实后确认。');});
bind('settle',async()=>{if(!previewData)throw new Error('请先生成预览');if(!confirm('确认按当前预览入账？本期结果不可覆盖，之后将自动生成下一期草稿。'))return;const input={...settleInput(),preview_token:previewData.token};const done=await api('rounds/'+getID()+'/settle',input,true);await refreshState();notice(done.number+'期已结算入账，下一期已进入待配置状态。通知送达请查看发送记录。');});
bindForm('player-form',async()=>{const id=Number($('player-tg').value);if(!Number.isSafeInteger(id)||id<=0)throw new Error('Telegram ID必须是有效正整数');await api('accounts',{telegram_id:id,name:$('player-name').value.trim(),enabled:$('player-enabled').checked},true);await refreshState();await loadPlayers();notice('玩家资格已保存，余额未改变。');});

bindForm('adjust-form',async()=>{const id=$('adjust-id').value.trim(),delta=Number($('adjust-delta').value);if(!Number.isSafeInteger(delta)||delta===0)throw new Error('调分变化量应为非零整数');const note=$('adjust-note').value.trim();if(!confirm('账户 '+id+'\n变化 '+signed(delta)+'\n备注 '+note+'\n确认记入账本？'))return;const a=await api('adjustments',{account_id:id,delta,note},true);await refreshState();await loadPlayers();$('adjust-delta').value='';$('adjust-note').value='';notice('调分成功，账户 '+a.id+' 当前余额 '+fmt(a.balance)+'。');});
bindForm('rules-form',async()=>{const r={payout:Array.from({length:11},(_,i)=>Number($('odd-'+i).value)),min_stake:Number($('min-stake').value),max_stake:Number($('max-stake').value),zero_triple:$('zero-triple').value==='true',confirmed:true};await api('rules',{expected_version:rulesVersion,rules:r},true);await refreshState();renderRules();notice('规则模板已保存，仅影响后续开盘期次。'+'玩家模式已启用。');});
bind('reload-rules',async()=>{if(!confirm('重新读取后端配置？会丢弃本页未保存修改。'))return;await refreshState();renderRules();notice('已读取后端配置。');});
bindForm('calc-form',async()=>{const r=await api('calculate',{damage:$('calc-damage').value.trim(),banker_damage:$('calc-banker').value.trim()});const hands=[['闲家',r.hand],...(r.banker?[['庄家',r.banker]]:[])];$('calc-result').innerHTML=table(['身份','原始伤害','处理后','牛型','最大数字','说明'],hands.map(([name,h])=>[name,esc(h.raw),esc(h.normalized||'—'),esc(h.label),esc(h.max_digit),esc(h.explanation)]))+(r.comparison?'<p class="summary">对庄结果：'+esc(states[r.comparison.outcome])+'；'+esc(r.comparison.reason)+'。'+(r.comparison.outcome==='WIN'?'闲家净盈利倍数：'+r.player_win_multiplier:'')+'</p>':'');notice('计算完成，未创建注单，也未修改余额。');});
bind('entries-load',async()=>{offsets.entries=0;await loadEntries();});
bind('reconcile',async()=>{const r=await api('reconcile');$('reconcile-result').innerHTML='<p class="summary">'+(r.balanced?'账本与冻结检查通过':'发现不一致')+'｜账户 '+r.account_count+'｜流水 '+r.entry_count+'｜待结算注单 '+r.pending_bets+'｜所有账户余额合计 '+r.balance_sum+'</p>'+r.issues.map(s=>'<p>'+esc(s)+'</p>').join('');});
for(const [name,size,loader] of [['players',50,loadPlayers],['history',10,loadHistory],['entries',50,loadEntries],['messages',50,loadMessages]]){bind(name+'-prev',async()=>{offsets[name]=Math.max(0,offsets[name]-size);await loader();});bind(name+'-next',async()=>{offsets[name]+=size;await loader();});}
document.addEventListener('click',e=>{const b=e.target.closest('button');if(!b||b.disabled)return;
 if(b.dataset.tab)busy(b,()=>showTab(b.dataset.tab));
 if(b.dataset.account){$('adjust-id').value=b.dataset.account;notice('已选择账户 '+b.dataset.account+'，尚未调分。');}
 if(b.dataset.player){$('player-tg').value=b.dataset.player;$('player-name').value=b.dataset.name;$('player-enabled').checked=b.dataset.enabled==='true';notice('已选择玩家，保存后才会修改资格。');}
 if(b.dataset.round)busy(b,async()=>{const r=await api('rounds/'+b.dataset.round);$('history-detail').innerHTML=r.result?renderPreview(r.result):'<p class="hint">该期尚未结算。</p>';});
 if(b.dataset.message)busy(b,async()=>{const action=b.dataset.resolve;let message_id=0;if(action==='retry'&&!confirm('请先核实Telegram。此操作可能重复发送一条已送达但结果未知的消息，确认重试？'))return;if(action==='ack'){const input=prompt('输入在Telegram核实的message_id。非期次卡片可以填0，期次卡片必须填写真实ID。','0');if(input===null)return;message_id=Number(input);if(!Number.isSafeInteger(message_id)||message_id<0)throw new Error('message_id必须是非负整数');}if(action==='skip'&&!confirm('明确跳过此通知？这不会撤销已经完成的下注或结算。'))return;await api('outbox/'+b.dataset.message+'/resolve',{action,message_id},true);await loadMessages();await refreshState();notice('发送任务处理已保存，账目没有重复结算。');});
});

async function loadRequests(){const d=await api('balance-requests?limit=100');$('requests-count').textContent='待处理上分：'+d.counts.credit+'｜待处理下分：'+d.counts.debit;$('requests-table').innerHTML=table(['编号','类型','TG ID','用户名','金额','申请时余额','当前余额','时间','状态','操作'],d.rows.map(x=>[x.id,x.kind==='CREDIT'?'上分':'下分',x.telegram_id,esc(x.username?'@'+x.username:'未设置'),tdnum(x.amount),tdnum(x.balance_at_request),tdnum(x.current_balance),when(x.created_at),requestStates[x.state],x.state==='PENDING'?'<button data-request="'+x.id+'" data-action="APPROVED">批准</button> <button data-request="'+x.id+'" data-action="REJECTED">拒绝</button>':'—']));}
async function loadChampions(){const d=await api('champions');championCatalog=d.champions||[];$('champion-status').textContent='Data Dragon版本：'+(d.version||'—')+'｜数量：'+championCatalog.length+'｜最后更新：'+when(d.updated_at)+'｜'+d.status;$('champions-table').innerHTML=table(['Riot ID','中文名','英文名','头像缓存'],championCatalog.map(c=>[c.id,esc(c.name),esc(c.english_name),esc(c.cache_path)]));}
async function loadBot(){const d=await api('bot-settings');$('bot-token').value='';$('bot-username').value=d.bot_username||'';$('bot-group').value=d.group_id||'';$('bot-topic').value=d.topic_id||'';$('bot-admin').value=d.admin_id||'';$('bot-support').value=d.support_username||'';$('bot-enabled').checked=d.enabled;$('bot-status').textContent='当前Token：'+d.token_mask+'｜最近保存：'+when(d.saved_at)+'｜'+(d.restart_required?'保存后需重启':'配置已生效');}
const oldShow=showTab;showTab=async function(tab){await oldShow(tab);if(tab==='requests')await loadRequests();if(tab==='champions')await loadChampions();if(tab==='bot')await loadBot();};
bind('requests-refresh',loadRequests);bind('champions-refresh',async()=>{await api('champions/refresh',{},true);await loadChampions();notice('英雄数据已刷新；失败时原缓存会保留。')});bind('bot-test',async()=>{const x=await api('bot-settings/test',{},true);notice('连接成功：@'+x.username)});bind('bot-test-group',async()=>{await api('bot-settings/test-group',{},true);notice('群测试消息已加入发送队列')});bindForm('bot-form',async()=>{await api('bot-settings',{token:$('bot-token').value,bot_username:$('bot-username').value,group_id:Number($('bot-group').value||0),topic_id:Number($('bot-topic').value||0),admin_id:Number($('bot-admin').value||0),support_username:$('bot-support').value,enabled:$('bot-enabled').checked,revision:Number(($('bot-form').dataset.revision||0))},true);await loadBot();notice('配置已原子保存；请按提示重启服务。')});
document.addEventListener('click',e=>{const b=e.target.closest('button');if(!b)return;if(b.dataset.request)busy(b,async()=>{const note=prompt('管理员备注（可留空）','')??'';await api('balance-requests/resolve',{id:Number(b.dataset.request),action:b.dataset.action,note},true);await loadRequests();notice('申请处理完成。')});});


bind('bot-restart',async()=>{if(!confirm('确定重启服务？未保存的页面修改不会保存。'))return;await api('bot-settings/restart',{},true);notice('服务正在重启，请稍候后刷新页面。');});
