'use strict';

// A timeout or an invalid response does not prove a write failed. Never replay
// writes here: callers retain their idempotency key for explicit reconciliation.
async function requestJSON(path, options, mutate, timeoutMS) {
 const attempts=options.method==='GET'&&!timeoutMS?2:1;
 for(let attempt=0;attempt<attempts;attempt++){
  const controller=new AbortController();
  const timer=setTimeout(()=>controller.abort(),timeoutMS||(mutate?45000:15000));
  let response,result,problem='';
  try{
   response=await fetch('/api/'+path,{...options,signal:controller.signal});
   const text=await response.text();
   try{result=JSON.parse(text);}catch{problem='接口返回空内容、非 JSON 或截断内容';}
   if(!problem&&(!result||typeof result.ok!=='boolean'||(result.ok&&!Object.hasOwn(result,'data'))))problem='接口响应格式不匹配';
  }catch(e){problem=e.name==='AbortError'?'请求超时':'连接中断';}
  finally{clearTimeout(timer);}
  if(!problem)return {response,result};
  if(attempt+1<attempts){await new Promise(resolve=>setTimeout(resolve,500));continue;}
  const status=response?'（HTTP '+response.status+'）':'';
  const id=response?.headers?.get('X-Request-ID');
  const detail=id?'；请求编号 '+id:'';
  throw new Error(problem+status+'。'+(mutate?'提交结果尚未确认，请保持输入不变，刷新数据核实后再重试。':'读取失败，请检查服务或网络后重试；本次读取不会修改数据。')+detail);
 }
}
