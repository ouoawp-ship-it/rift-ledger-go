const test=require('node:test');
const assert=require('node:assert/strict');
const {layoutMessageBlocks}=require('../internal/httpapi/web/message-layout.js');
test('image and following text share one caption; preserves multiple image order',()=>{
 const input=[{type:'text',text:'前言'},{type:'image',image_id:'one'},{type:'text',text:'期数 🔔'},{type:'text',text:'正文'},{type:'image',image_id:'two'},{type:'text',text:'结束'}];
 assert.deepEqual(layoutMessageBlocks(input),[input[0],{type:'image',image_id:'one',text:'期数 🔔\n\n正文'},{type:'image',image_id:'two',text:'结束'}]);
 assert.equal(input[1].text,undefined);
});
test('caption overflow preserves emoji and all text within Telegram limits',()=>{
 const text='中'.repeat(1023)+'🎉'+'后'.repeat(4100);
 const parts=layoutMessageBlocks([{type:'image',image_id:'one'},{type:'text',text},{type:'text',text:'末段'}]);
 assert.equal(parts.map(p=>p.text).join(''),text+'\n\n末段');
 assert.equal(parts[0].text.length,1023);
 for(const p of parts)assert.ok(p.text.length<=(p.type==='image'?1024:4000));
 assert.ok(parts[1].text.startsWith('🎉'));
});
