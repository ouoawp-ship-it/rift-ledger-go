const fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
let playwright;try{playwright=require('playwright')}catch{playwright=require(path.join(path.dirname(process.execPath),'../node_modules/playwright'))}
(async()=>{
 const c=JSON.parse(fs.readFileSync(0,'utf8')),browser=await playwright.chromium.launch({channel:'msedge',headless:true});
 try{
  const page=await browser.newPage({viewport:{width:1512,height:1100}}),errors=[];
  page.on('pageerror',e=>errors.push(e.message));
  await page.goto(c.base);await page.locator('#token').fill(c.token);await page.locator('#login-form button').click();
  await page.locator('#workspace').waitFor({state:'visible'});await page.locator('.sidebar [data-tab="players"]').click();
  const ready=async(text)=>{await page.waitForFunction(t=>document.querySelector('#players-filter-summary').textContent.includes(t)&&document.querySelector('#players-table').getAttribute('aria-busy')==='false',text)};
  await ready('有余额玩家 · 57 人');assert.equal(await page.locator('#players-table tbody tr').count(),50);
  await page.locator('#players-next').click();await page.waitForFunction(()=>document.querySelector('#players-page').textContent==='第 2 / 2 页');assert.equal(await page.locator('#players-table tbody tr').count(),7);
  await page.locator('[name="players-balance-filter"][value="zero"]').check();await ready('零分玩家 · 5 人');assert.equal(await page.locator('#players-table tbody tr').count(),5);assert(await page.locator('#players-prev').isDisabled());
  await page.locator('[name="players-balance-filter"][value="all"]').check();await ready('全部玩家 · 62 人');
  await page.locator('#players-search').fill('@tiny_balance');await ready('全部玩家 · 1 人');assert((await page.locator('#players-table').innerText()).includes('0.001'));
  await page.locator('[name="players-balance-filter"][value="zero"]').check();await ready('零分玩家 · 0 人');assert(await page.locator('.players-empty').isVisible());
  await page.locator('[name="players-balance-filter"][value="positive"]').check();await ready('有余额玩家 · 1 人');await page.locator('[data-account="tg:100000107"]').click();assert(await page.locator('#players-adjust').evaluate(e=>e.open));assert.equal(await page.locator('#adjust-id').inputValue(),'tg:100000107');
  await page.locator('#players-adjust summary').click();
  // A delayed earlier request must not overwrite a later selection.
  let delayed=false;
  await page.route('**/api/accounts?**',async route=>{if(!delayed&&route.request().url().includes('balance=all')){delayed=true;await new Promise(r=>setTimeout(r,700))}await route.continue()});
  await page.locator('[name="players-balance-filter"][value="all"]').check();await page.waitForTimeout(100);await page.locator('[name="players-balance-filter"][value="zero"]').check();await ready('零分玩家 · 0 人');await page.waitForTimeout(900);assert((await page.locator('#players-filter-summary').innerText()).startsWith('零分玩家'));
  await page.unroute('**/api/accounts?**');
  await page.locator('#players-search').fill('10000010');await page.locator('[name="players-balance-filter"][value="positive"]').check();await ready('有余额玩家 · 7 人');assert.equal(await page.locator('#players-total').innerText(),'62');
  await page.screenshot({path:path.join(c.artifacts,'player-filter-desktop.png'),fullPage:true});
  await page.setViewportSize({width:390,height:844});await page.screenshot({path:path.join(c.artifacts,'player-filter-mobile.png'),fullPage:true});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
  assert.deepEqual(errors,[]);console.log('PASS: real API, pagination, all/zero/positive, 0.001, username search, empty results, adjustment entry, stale response, global stats, mobile overflow');
 }finally{await browser.close()}
})().catch(e=>{console.error(e);process.exitCode=1});
