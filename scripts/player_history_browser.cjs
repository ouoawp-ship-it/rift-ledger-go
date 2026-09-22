'use strict';
// Consumes ephemeral fixture configuration on stdin; never a production token.
const fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
let playwright;try{playwright=require('playwright');}catch{playwright=require(path.join(path.dirname(process.execPath),'../node_modules/playwright'));}
(async()=>{
 const c=JSON.parse(fs.readFileSync(0,'utf8'));
 const browser=await playwright.chromium.launch({headless:true,...(c.browser?{executablePath:c.browser}:{}),args:['--no-sandbox']});
 try{
  const page=await browser.newPage({viewport:{width:1440,height:1000},reducedMotion:'reduce'}),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.goto(c.base);await page.locator('#token').fill(c.token);await page.locator('#login-form button').click();
  await page.locator('#workspace').waitFor({state:'visible'});
  await page.locator('.sidebar [data-tab="players"]').click();
  await page.locator('[data-player-history="tg:111"]').first().click();
  await page.locator('#ph-results').getByText('120.001',{exact:false}).waitFor();
  let text=await page.locator('#ph-results').innerText();
  assert(text.includes('中奖')&&text.includes('未中奖')&&text.includes('流局 / 已退回'));
  assert(text.includes('100.001')&&text.includes('-100.001'));
  await page.screenshot({path:path.join(c.artifacts,'player-history-desktop.png'),fullPage:true});
  await page.locator('#ph-filter').selectOption('WIN');await page.locator('#ph-filter-form button[type=submit]').click();
  await page.waitForFunction(()=>document.querySelectorAll('#ph-results tbody tr').length===1);
  assert(!(await page.locator('#ph-results').innerText()).includes('未中奖'));
  await page.locator('[data-history-view="entries"]').click();
  await page.locator('#ph-next').waitFor();await page.waitForFunction(()=>!document.querySelector('#ph-next').disabled);
  assert.equal(await page.locator('#ph-results tbody tr').count(),25);
  const first=await page.locator('#ph-results').innerText();await page.locator('#ph-next').click();
  await page.waitForFunction(()=>document.querySelector('#ph-page').textContent.includes('第 2 页'));
  assert.notEqual(await page.locator('#ph-results').innerText(),first);
  assert(await page.locator('#ph-next').isDisabled());
  await page.locator('[data-history-view="requests"]').click();
  await page.locator('#ph-results').getByText('已拒绝',{exact:true}).waitFor();
  assert((await page.locator('#ph-results').innerText()).includes('1.234'));
  await page.locator('#ph-from').fill('2000-01-01');await page.locator('#ph-to').fill('2000-01-01');await page.locator('#ph-filter-form button[type=submit]').click();
  await page.locator('#ph-results').getByText('暂无符合条件的记录').waitFor();
  await page.locator('#ph-reset').click();await page.locator('#ph-results').getByText('已拒绝',{exact:true}).waitFor();
  await page.locator('#ph-search').fill('@player_one');await page.locator('#ph-search-form button').click();
  await page.locator('#ph-search-result [data-player-history="tg:111"]').waitFor();
  await page.locator('#ph-search-result [data-player-history="tg:111"]').click();
  await page.locator('#ph-results').getByText('120.001',{exact:false}).waitFor();
  for(const viewport of [{width:375,height:900},{width:812,height:375}]){
   await page.setViewportSize(viewport);assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  }
  await page.setViewportSize({width:375,height:900});await page.screenshot({path:path.join(c.artifacts,'player-history-mobile.png'),fullPage:true});
  assert.equal(await page.evaluate(()=>localStorage.length+sessionStorage.length),0);
  assert.deepEqual(errors,[]);
  console.log(JSON.stringify({passed:true,real_telegram:false,checks:['player row opens history','persisted win/loss/void and three decimal amounts','outcome filter','ledger cursor pagination','requests and rejection status','inclusive date empty state and reset','username search','375px and landscape without page overflow','no script errors or browser token persistence']},null,2));
 }finally{await browser.close();}
})().catch(e=>{console.error(e.message);process.exit(1);});
