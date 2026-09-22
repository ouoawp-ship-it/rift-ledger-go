'use strict';
// Keep the preview's UTF-16 limits and block ordering aligned with layoutMessageBlocks.
function layoutMessageBlocks(blocks) {
 const split=(text,limit)=>{const out=[];let part='',units=0;for(const char of text){if(units+char.length>limit){out.push(part);part='';units=0;}part+=char;units+=char.length;}if(part)out.push(part);return out;};
 const out=[];
 for(let i=0;i<blocks.length;i++){
  const b=blocks[i];
  if(b.type!=='image'){for(const text of split(b.text||'',4000))out.push({type:'text',text});continue;}
  const texts=[];
  while(i+1<blocks.length&&blocks[i+1].type==='text')texts.push(blocks[++i].text||'');
  const chunks=split(texts.join('\n\n'),1024);
  out.push({...b,text:chunks[0]||''});
  for(const text of split(chunks.slice(1).join(''),4000))out.push({type:'text',text});
 }
 return out;
}
if(typeof module!=='undefined')module.exports={layoutMessageBlocks};
