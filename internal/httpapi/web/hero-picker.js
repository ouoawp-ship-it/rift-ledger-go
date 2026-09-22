'use strict';
let heroPickerSlot=1,heroPickerRound='';
const heroDialog=$('hero-dialog');
function heroPickerIDs(){return Array.from({length:5},(_,i)=>$('hero-id-'+(i+1)).value);}
function heroConfigurationDirty(){
 const r=state?.active_round;if(!r)return false;
 return heroPickerIDs().some((id,i)=>id!==(r.heroes?.[i]?.id||''))||Number(document.querySelector('input[name=banker]:checked')?.value||0)!==r.banker;
}
function syncHeroPicker(){
 const r=state?.active_round,editable=r?.state==='DRAFT',ids=heroPickerIDs();
 const banker=Number(document.querySelector('input[name=banker]:checked')?.value||0);
 for(let n=1;n<=5;n++){
  const c=championCatalog.find(c=>c.id===ids[n-1]),name=$('hero-name-'+n).value;
  $('hero-select-'+n).disabled=!editable;
  $('banker-'+n).disabled=!editable||!ids[n-1];
  $('hero-label-'+n).textContent=name||'选择英雄';
  $('hero-subtitle-'+n).textContent=name?(c?.title||c?.english_name||ids[n-1]):'点击搜索或浏览头像';
  $('hero-role-'+n).textContent=banker===n?'庄家':banker?'闲家':'未选庄家';
  $('hero-slot-'+n).classList.toggle('is-banker',banker===n);
  $('hero-slot-'+n).classList.toggle('is-selected',!!ids[n-1]);
  $('hero-select-'+n).setAttribute('aria-label',n+'号英雄：'+(name||'未选择')+(editable?'，点击选择':''));
  setChampionAvatar(n,ids[n-1]);
 }
 const count=ids.filter(Boolean).length,dirty=heroConfigurationDirty();
 $('hero-selection-status').textContent=!r?'先创建首期草稿':!editable?'本期英雄已锁定':`已选 ${count}/5 · ${banker?banker+'号庄家':'请指定庄家'}${dirty?' · 未保存':''}`;
 $('open-round').disabled=!editable||dirty||count!==5||!banker;
 $('open-round').title=dirty?'请先保存英雄与庄家':'';
 if(heroDialog.open&&(!editable||r.id!==heroPickerRound))heroDialog.close();
}
function renderHeroPickerSlots(){
 $('hero-dialog-title').textContent='选择 '+heroPickerSlot+' 号英雄';
 $('hero-dialog-slots').innerHTML=heroPickerIDs().map((id,i)=>'<button type="button" data-picker-slot="'+(i+1)+'" aria-pressed="'+(heroPickerSlot===i+1)+'"><span>'+ (i+1)+'号</span><strong>'+esc($('hero-name-'+(i+1)).value||'未选择')+'</strong></button>').join('');
 $('hero-dialog-progress').textContent='已选择 '+heroPickerIDs().filter(Boolean).length+' / 5 位英雄 · 选择后请保存配置';
}
function renderHeroPickerResults(){
 const query=$('hero-picker-search').value.trim();
 const matches=query?championMatches(query):[...championCatalog].sort((a,b)=>a.name.localeCompare(b.name,'zh-CN'));
 const ids=heroPickerIDs();
 $('hero-picker-count').textContent=query?'找到 '+matches.length+' 位英雄':'全部 '+matches.length+' 位英雄';
 const grid=$('hero-picker-results');grid.replaceChildren();grid.scrollTop=0;
 if(!matches.length){const hint=document.createElement('p');hint.className='hero-empty';hint.textContent=championCatalog.length?'没有找到英雄，请换用中文名、称号或英文名。':'英雄数据尚未加载，请先到“英雄数据”页面刷新，再返回选择。';grid.append(hint);return;}
 for(const c of matches){
  const position=ids.indexOf(c.id)+1,used=position>0&&position!==heroPickerSlot;
  const button=document.createElement('button');button.type='button';button.className='hero-pick-option';button.disabled=used;button.dataset.champion=c.id;
  button.setAttribute('aria-label',c.name+(c.title?' · '+c.title:'')+(position?' · 已选'+position+'号':''));
  button.setAttribute('aria-pressed',String(position===heroPickerSlot));
  button.innerHTML='<span class="hero-pick-portrait"><span aria-hidden="true">'+esc(c.name.slice(0,1))+'</span>'+(c.image_url?'<img src="'+esc(c.image_url)+'" alt="" loading="lazy">':'')+'</span><strong>'+esc(c.name)+'</strong><small>'+esc(c.title||c.english_name||c.id)+'</small><span class="hero-pick-badge">'+(position?'已选 '+position+'号':'选择')+'</span>';
  const img=button.querySelector('img');if(img)img.addEventListener('error',()=>img.hidden=true);
  button.addEventListener('click',()=>selectPickerChampion(c));grid.append(button);
 }
}
function selectPickerChampion(c){
 if(state?.active_round?.state!=='DRAFT'||state.active_round.id!==heroPickerRound){heroDialog.close();return;}
 if(heroPickerIDs().some((id,i)=>id===c.id&&i+1!==heroPickerSlot))return;
 const n=heroPickerSlot;
 $('hero-id-'+n).value=c.id;$('hero-name-'+n).value=c.name;$('hero-search-'+n).value=c.name;
 syncHeroPicker();
 const ids=heroPickerIDs();let next=0;
 for(let offset=1;offset<=5;offset++){const slot=(n-1+offset)%5+1;if(!ids[slot-1]){next=slot;break;}}
 if(!next){heroDialog.close();return;}
 heroPickerSlot=next;$('hero-picker-search').value='';renderHeroPickerSlots();renderHeroPickerResults();$('hero-picker-search').focus();
}
function openHeroPicker(n){
 if(state?.active_round?.state!=='DRAFT')return;
 heroPickerSlot=n;heroPickerRound=state.active_round.id;$('hero-picker-search').value='';renderHeroPickerSlots();renderHeroPickerResults();heroDialog.showModal();$('hero-picker-search').focus();
}
$('hero-inputs').addEventListener('click',e=>{const b=e.target.closest('[data-hero-slot]');if(b)openHeroPicker(Number(b.dataset.heroSlot));});
$('hero-inputs').addEventListener('change',syncHeroPicker);
$('hero-dialog-slots').addEventListener('click',e=>{const b=e.target.closest('[data-picker-slot]');if(!b)return;heroPickerSlot=Number(b.dataset.pickerSlot);$('hero-picker-search').value='';renderHeroPickerSlots();renderHeroPickerResults();$('hero-picker-search').focus();});
$('hero-picker-search').addEventListener('input',renderHeroPickerResults);
$('hero-picker-search').addEventListener('keydown',e=>{if(e.isComposing)return;if(e.key==='Enter'){e.preventDefault();$('hero-picker-results').querySelector('button:not(:disabled)')?.click();}else if(e.key==='ArrowDown'){e.preventDefault();$('hero-picker-results').querySelector('button:not(:disabled)')?.focus();}});
$('hero-picker-clear').onclick=()=>{$('hero-picker-search').value='';renderHeroPickerResults();$('hero-picker-search').focus();};
$('hero-dialog-close').onclick=$('hero-dialog-done').onclick=()=>heroDialog.close();
heroDialog.addEventListener('close',()=>{syncHeroPicker();$('hero-select-'+heroPickerSlot).focus();});
for(let n=1;n<=5;n++)$('hero-avatar-'+n).addEventListener('error',e=>{e.target.hidden=true;});
