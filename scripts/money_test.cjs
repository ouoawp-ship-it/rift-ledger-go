const test=require('node:test');
const assert=require('node:assert/strict');
const vm=require('node:vm');
const fs=require('node:fs');
const source=fs.readFileSync('internal/httpapi/web/app.js','utf8');
const ctx=vm.createContext({});
vm.runInContext(source.slice(source.indexOf('const fmt='),source.indexOf('const when='))+'\nthis.moneyFormat=fmt;this.signedMoney=signed;',ctx);
test('money rendering preserves three decimals including large aggregate strings',()=>{
 for(const [value,want] of [[1000.199,'1,000.199'],['1120.200','1,120.200'],[1,'1.000'],[0,'0.000'],[-0.001,'-0.001'],['999999999999.999','999,999,999,999.999'],['999999999999999.999','999,999,999,999,999.999']]) assert.equal(ctx.moneyFormat(value),want);
 assert.equal(ctx.signedMoney('0.001'),'+0.001');
 assert.equal(ctx.signedMoney('-0.001'),'-0.001');
});
