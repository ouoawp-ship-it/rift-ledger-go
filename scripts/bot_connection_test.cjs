// Run with: node --test scripts/bot_connection_test.cjs
const {test}=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const path=require('node:path');
const source=fs.readFileSync(path.join(__dirname,'../internal/httpapi/web/bot-connection.js'),'utf8');

function setup(){
 const nodes=new Map(),events={},timeouts=new Map(),intervals=new Map();
 let nextID=0,clock=1000;
 const context=vm.createContext({
  token:'test-token',Date:{now:()=>clock},AbortController,
  $:id=>{if(!nodes.has(id))nodes.set(id,{dataset:{}});return nodes.get(id);},when:String,
  document:{hidden:false,addEventListener:(name,handler)=>events[name]=handler},
  window:{addEventListener:(name,handler)=>events[name]=handler},
  setTimeout:fn=>{timeouts.set(++nextID,fn);return nextID;},clearTimeout:id=>timeouts.delete(id),
  setInterval:fn=>{intervals.set(++nextID,fn);return nextID;},clearInterval:id=>intervals.delete(id),
  fetch:async()=>({ok:true,json:async()=>({ok:true,data:{state:'online',message:'接收正常',updated_at:100}})})
 });
 vm.runInContext(source,context);
 return {context,nodes,events,timeouts,intervals,tick:ms=>clock+=ms,state:()=>nodes.get('bot-connection')?.dataset.state};
}

test('real responses control the light; network failure and recovery update it',async()=>{
 const s=setup();
 await s.context.pollBotConnection();
 assert.equal(s.state(),'online');
 assert.equal(s.nodes.get('bot-connection-time').textContent.includes('100'),true);
 s.context.fetch=async()=>{throw new Error('offline');};
 await s.context.pollBotConnection();
 assert.equal(s.state(),'unreachable');
 for(const state of ['connecting','disabled','error','stale','online']){
  s.context.fetch=async()=>({ok:true,json:async()=>({ok:true,data:{state,message:state}})});
  await s.context.pollBotConnection();assert.equal(s.state(),state);
 }
 s.context.fetch=async()=>({ok:false,json:async()=>({ok:false})});
 await s.context.pollBotConnection();assert.equal(s.state(),'unreachable');
});

test('slow requests time out and overlapping polls are suppressed',async()=>{
 const s=setup();let calls=0;
 s.context.fetch=(_url,{signal})=>{calls++;return new Promise((resolve,reject)=>signal.addEventListener('abort',()=>reject(new Error('timeout'))));};
 const polling=s.context.pollBotConnection();
 await s.context.pollBotConnection();assert.equal(calls,1);
 [...s.timeouts.values()][0]();await polling;
 assert.equal(s.state(),'unreachable');assert.equal(s.timeouts.size,0);
});

test('old green status is cleared on visibility return and offline; logout ignores late responses',async()=>{
 const s=setup();await s.context.pollBotConnection();
 s.events.offline();assert.equal(s.state(),'unreachable');
 let resolveFetch;
 s.context.fetch=()=>new Promise(resolve=>resolveFetch=resolve);
 s.events.visibilitychange();assert.equal(s.state(),'connecting');
 s.context.stopBotConnectionWatcher();s.context.token='';
 resolveFetch({ok:true,json:async()=>({ok:true,data:{state:'online',message:'late'}})});
 await new Promise(resolve=>setImmediate(resolve));
 assert.notEqual(s.state(),'online');assert.equal(s.intervals.size,0);
});
