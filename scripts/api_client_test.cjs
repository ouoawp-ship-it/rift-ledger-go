const test=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
function setup(fetch){
 const c=vm.createContext({fetch,AbortController,setTimeout:(f,n)=>setTimeout(f,n===500?0:n),clearTimeout,console,crypto:require('node:crypto').webcrypto});
 vm.runInContext(fs.readFileSync('internal/httpapi/web/api-client.js','utf8'),c);
 const s=fs.readFileSync('internal/httpapi/web/app.js','utf8');
 vm.runInContext("const pending=new Map();let token='isolated-test';function requestID(){return crypto.randomUUID();}\n"+s.slice(s.indexOf('async function api('),s.indexOf('async function busy(')),c);
 return c;
}
test('GET retries malformed response and never calls it an uncertain write',async()=>{
 let calls=0;const c=setup(async()=>{calls++;return new Response('<html>bad gateway</html>',{status:502,headers:{'X-Request-ID':'test-id'}})});
 await assert.rejects(c.api('state'),/读取失败.*不会修改数据.*test-id/);assert.equal(calls,2);
});
test('GET recovers after truncated JSON',async()=>{
 let calls=0;const c=setup(async()=>new Response(++calls===1?'{"ok":':JSON.stringify({ok:true,data:{ready:true}})));
 assert.equal((await c.api('state')).ready,true);assert.equal(calls,2);
});
test('uncertain write is not retried automatically and keeps its business key',async()=>{
 const ids=[];const c=setup(async(url,options)=>{ids.push(options.headers['Idempotency-Key']);return new Response(ids.length===1?'<html>502</html>':JSON.stringify({ok:true,data:{balance:10}}));});
 await assert.rejects(c.api('adjustments',{delta:10},true),/提交结果尚未确认/);assert.equal(ids.length,1);
 assert.equal((await c.api('adjustments',{delta:10},true)).balance,10);assert.equal(ids[0],ids[1]);
});
test('valid 401 uses the server message',async()=>{
 const c=setup(async()=>new Response(JSON.stringify({ok:false,error:'管理员密钥错误'}),{status:401}));
 await assert.rejects(c.api('state'),/管理员密钥错误/);
});
test('incorrect success envelope is rejected',async()=>{
 const c=setup(async()=>new Response('{"ok":true}'));
 await assert.rejects(c.api('adjustments',{},true),/格式不匹配/);
});
test('network failure does not replay a write',async()=>{
 let calls=0;const c=setup(async()=>{calls++;throw new TypeError('network error')});
 await assert.rejects(c.api('adjustments',{},true),/提交结果尚未确认/);assert.equal(calls,1);
});

test('acknowledged credit is cleared before a following read can fail',async()=>{
 const fields={'adjust-id':{value:'tg:1'},'adjust-delta':{value:'100'},'adjust-note':{value:'test'}};
 let handler;
 const c=vm.createContext({$:(id)=>fields[id],Number,confirm:()=>true,signed:String,fmt:String,notice:()=>{},api:async()=>({id:'tg:1',balance:100}),refreshState:async()=>{throw new Error('read failed')},loadPlayers:async()=>{},bindForm:(id,fn)=>{handler=fn}});
 const line=fs.readFileSync('internal/httpapi/web/app.js','utf8').split('\n').find(x=>x.startsWith("bindForm('adjust-form'"));
 vm.runInContext(line,c);
 await assert.rejects(handler(),/read failed/);
 assert.equal(fields['adjust-delta'].value,'');assert.equal(fields['adjust-note'].value,'');
});
