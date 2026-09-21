'use strict';
(() => {
 let catalog=[],current=null,blocks=[],dirty=false,owner='',focusIndex=0,working=false;
 const images=new Map(),copy=x=>JSON.parse(JSON.stringify(x));
 function mark(){dirty=true;$('template-save-state').textContent='有未保存的修改';}
 function payload(){return {id:current.id,revision:current.revision,blocks:copy(blocks)};}
 function setWorking(value){working=value;document.querySelectorAll('#template-editor button,#template-editor select,#template-editor textarea,#template-editor input').forEach(el=>el.disabled=value);if(!value){$('template-blocks').querySelectorAll('[data-block]').forEach((el,i)=>{el.querySelector('[data-action="up"]').disabled=i===0;el.querySelector('[data-action="down"]').disabled=i===blocks.length-1;});}}
 async function run(fn){if(working)return;setWorking(true);try{await fn();}catch(e){notice(e.message,true);$('template-save-state').textContent=e.message;}finally{setWorking(false);}}
 async function imageURL(id){
  if(!images.has(id))images.set(id,(async()=>{const r=await fetch('/api/message-images/'+encodeURIComponent(id),{headers:{Authorization:'Bearer '+token},signal:AbortSignal.timeout(15000)});if(!r.ok)throw new Error('图片读取失败，请重新打开此页面');return URL.createObjectURL(await r.blob());})().catch(e=>{images.delete(id);throw e;}));
  return images.get(id);
 }
 function hydrateImages(root){root.querySelectorAll('img[data-image]').forEach(img=>{imageURL(img.dataset.image).then(url=>{if(img.isConnected)img.src=url;}).catch(()=>{if(img.isConnected)img.replaceWith(document.createTextNode('图片暂时无法预览，请重新载入'));});});}
 function preview(rendered){
  if(!current)return;
  const values=Object.fromEntries(current.variables.map(v=>[v.name,v.example]));
  const parts=rendered||blocks.map(b=>b.type==='text'?{...b,text:b.text.replace(/\{([^{}\n]+)\}/g,(m,k)=>values[k]??m)}:b);
  $('template-preview').innerHTML=parts.map(b=>b.type==='image'?'<div class="message-bubble image-bubble"><img data-image="'+esc(b.image_id)+'" alt="自定义推送图片"></div>':'<div class="message-bubble">'+esc(b.text||'在左侧输入消息内容…')+'</div>').join('');
  hydrateImages($('template-preview'));
 }
 function render(){
  $('template-destination').textContent='发送至 · '+current.destination;
  $('template-variables').innerHTML=current.variables.map(v=>'<div class="variable-row"><button type="button" data-variable="'+esc(v.name)+'">{'+esc(v.name)+'}</button><span><small>'+(v.required?'必须保留':'可选变量')+'</small><span class="variable-example">= '+esc(v.example)+'</span></span></div>').join('');
  $('template-blocks').innerHTML=blocks.map((b,i)=>'<section class="message-block" data-block="'+i+'"><div class="block-heading"><strong>'+String(i+1).padStart(2,'0')+' · '+(b.type==='text'?'文字段落':'图片')+'</strong><div><button type="button" data-action="up" aria-label="第'+(i+1)+'段上移"'+(i===0?' disabled':'')+'>↑</button><button type="button" data-action="down" aria-label="第'+(i+1)+'段下移"'+(i===blocks.length-1?' disabled':'')+'>↓</button><button type="button" data-action="remove">删除</button></div></div>'+(b.type==='text'?'<textarea aria-label="第'+(i+1)+'段消息文字" rows="7" spellcheck="false">'+esc(b.text)+'</textarea>':'<img data-image="'+esc(b.image_id)+'" alt="第'+(i+1)+'段推送图片">')+'</section>').join('');
  hydrateImages($('template-blocks'));preview();
 }
 function select(id){current=catalog.find(t=>t.id===id);blocks=copy(current.blocks);dirty=false;focusIndex=blocks.findIndex(b=>b.type==='text');$('template-kind').value=id;$('template-save-state').textContent=current.revision?'已保存，供新消息使用':'正在使用默认内容';render();}
 async function load(force=false){
  if(owner!==token){images.forEach(p=>p.then(URL.revokeObjectURL).catch(()=>{}));images.clear();catalog=[];current=null;owner=token;dirty=false;}
  if(catalog.length&&!force){hydrateImages($('template-blocks'));preview();return;}
  const data=await api('message-templates');catalog=data;
  $('template-kind').innerHTML=data.map(t=>'<option value="'+esc(t.id)+'">'+esc(t.name)+' · '+esc(t.destination)+'</option>').join('');
  select(current?.id||'round_close');
 }
 function insert(text){
  if(!current||working)return;
  let i=focusIndex;if(!blocks[i]||blocks[i].type!=='text')i=blocks.findIndex(b=>b.type==='text');
  if(i<0){notice('请先添加文字段落',true);return;}
  const area=$('template-blocks').querySelector('[data-block="'+i+'"] textarea');
  const start=area.selectionStart,end=area.selectionEnd;area.setRangeText(text,start,end,'end');blocks[i].text=area.value;area.focus();mark();preview();
 }
 $('template-kind').addEventListener('change',e=>{if(dirty&&!confirm('当前内容尚未保存，切换后将丢弃修改。继续切换？')){e.target.value=current.id;return;}select(e.target.value);});
 $('template-variables').addEventListener('click',e=>{const b=e.target.closest('[data-variable]');if(b)insert('{'+b.dataset.variable+'}');});
 $('template-emojis').addEventListener('click',e=>{const b=e.target.closest('button');if(b)insert(b.textContent);});
 $('template-blocks').addEventListener('focusin',e=>{if(e.target.matches('textarea'))focusIndex=Number(e.target.closest('[data-block]').dataset.block);});
 $('template-blocks').addEventListener('input',e=>{if(e.target.matches('textarea')){blocks[Number(e.target.closest('[data-block]').dataset.block)].text=e.target.value;mark();preview();}});
 $('template-blocks').addEventListener('click',e=>{const b=e.target.closest('[data-action]');if(!b||working)return;const i=Number(b.closest('[data-block]').dataset.block),action=b.dataset.action;
  if(action==='remove'){if(blocks[i].type==='text'&&blocks.filter(x=>x.type==='text').length===1){notice('至少保留一个文字段落',true);return;}blocks.splice(i,1);}
  else {const j=i+(action==='up'?-1:1);if(j<0||j>=blocks.length)return;[blocks[i],blocks[j]]=[blocks[j],blocks[i]];}
  focusIndex=blocks.findIndex(x=>x.type==='text');mark();render();
 });
 $('template-add-text').onclick=()=>{if(!current||working)return;if(blocks.length>=8){notice('最多8个内容段落',true);return;}blocks.push({type:'text',text:''});mark();render();$('template-blocks').lastElementChild.querySelector('textarea').focus();};
 $('template-add-image').onclick=()=>{if(current&&!working)$('template-upload').click();};
 $('template-upload').onchange=e=>{const file=e.target.files[0];e.target.value='';if(!file)return;run(async()=>{
  if(blocks.length>=8||blocks.filter(b=>b.type==='image').length>=3)throw new Error('最多8个内容段落，其中最多3张图片');
  if(file.size>2*1024*1024)throw new Error('图片不能超过2MB');
  const {response,result}=await requestJSON('message-images',{method:'POST',headers:{Authorization:'Bearer '+token,'Content-Type':file.type},body:file},true,45000);
  if(!response.ok||!result.ok)throw new Error(result.error||'图片上传失败');blocks.push({type:'image',image_id:result.data.id});mark();render();
 });};
 $('template-default').onclick=()=>{if(!current||working||!confirm('恢复此类消息的默认内容？点击保存设置后才会生效。'))return;blocks=copy(current.defaults);mark();render();};
 $('template-reload').onclick=()=>{if(dirty&&!confirm('重新载入会丢弃未保存的修改，是否继续？'))return;run(()=>load(true));};
 $('template-preview-button').onclick=()=>run(async()=>{if(!current)return;preview(await api('message-templates/preview',payload()));$('template-save-state').textContent='内容校验通过；预览使用示例数据，未发送消息';});
 $('template-save').onclick=()=>run(async()=>{if(!current)return;const saved=await api('message-templates',payload(),true);catalog[catalog.findIndex(t=>t.id===saved.id)]=saved;current=saved;blocks=copy(saved.blocks);dirty=false;render();$('template-save-state').textContent='已保存，新消息立即使用此设置';notice('消息设置已保存，无需重启机器人。');});
 window.addEventListener('beforeunload',e=>{if(dirty){e.preventDefault();e.returnValue='';}});
 const originalShow=showTab;showTab=async function(tab){await originalShow(tab);if(tab==='templates')await run(()=>load());};
})();
