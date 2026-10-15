// Synthetic desktop Chromium + real WebRTC loopback. Never Android/MediaMTX acceptance.
// Usage: node frontend/tools/phone-capture-probe.mjs <built-site-url> <installed-playwright-directory>
import { createRequire } from 'node:module';
import path from 'node:path';
import fs from 'node:fs/promises';
import assert from 'node:assert/strict';
if (!process.argv[3]) throw new Error('Pass an already installed Playwright directory; no dependency is installed.');
const require = createRequire(path.resolve(process.argv[3], 'package.json'));
const { chromium } = require(path.resolve(process.argv[3]));
const base = process.argv[2] ?? 'http://127.0.0.1:18767';
const browser = await chromium.launch({
  executablePath: process.env.EDGE_PATH ?? 'C:\\Program Files (x86)\\Microsoft\\Edge\\Application\\msedge.exe',
  headless: true, args: ['--use-fake-device-for-media-stream', '--use-fake-ui-for-media-stream', '--autoplay-policy=no-user-gesture-required'],
});
const result = { boundary: 'Desktop synthetic camera/microphone and in-browser WHIP mock with real WebRTC; no Android or real server recording', checks: [], codecs: [], browser: browser.version() };
const context = await browser.newContext({ viewport: { width: 390, height: 844 } });
await context.addInitScript(() => {
  window.fixture = { exchange: 0, publish: 0, deleted: 0, stop: 0, failStop: false, state: 'waiting', tracks: 0 };
  const fixture = window.fixture;
  const original = window.fetch.bind(window);
  const data = value => new Response(JSON.stringify({ data: value, request_id: 'synthetic-browser-only' }), { headers: { 'Content-Type': 'application/json' } });
  const capture = () => ({ id: 'synthetic-capture', session_id: 'synthetic-lesson', state: fixture.state,
    connection_state: fixture.tracks === 2 ? 'connected' : 'unknown', recording_state: fixture.state === 'ready' ? 'complete' : fixture.state,
    audio_track: fixture.tracks === 2 ? 'present' : 'unknown', video_track: fixture.tracks === 2 ? 'present' : 'unknown',
    media_asset_id: fixture.state === 'ready' ? 'synthetic-media-not-a-formal-id' : null, media_prepare_run_id: null,
    error_code: null, end_reason: null, allowed_actions: ['read', 'stop'] });
  window.fetch = async (input, init) => {
    const url = String(input);
    if (url.endsWith('/capture-pairings/exchange')) {
      fixture.exchange++;
      return data({ live_capture_id: 'synthetic-capture', session_id: 'synthetic-lesson', whip_url: location.origin + '/media/synthetic/whip',
        publish_token: 'synthetic-publish', control_token: 'synthetic-control', expires_at: '2099-01-01T00:00:00Z' });
    }
    if (init?.method === 'DELETE') { fixture.deleted++; fixture.receiver?.close(); return new Response(null, {status:204}); }
    if (url.endsWith('/media/synthetic/whip')) {
      fixture.publish++;
      if (init.headers.Authorization !== 'Bearer synthetic-publish') throw Error('Wrong WHIP token');
      const receiver = new RTCPeerConnection(); fixture.receiver = receiver;
      receiver.ontrack = () => { fixture.tracks++; };
      await receiver.setRemoteDescription({type:'offer',sdp:init.body});
      await receiver.setLocalDescription(await receiver.createAnswer());
      if (receiver.iceGatheringState !== 'complete') await new Promise((resolve,reject) => {
        const timer=setTimeout(()=>reject(Error('Synthetic receiver ICE timeout')),10000);
        receiver.onicegatheringstatechange=()=>{if(receiver.iceGatheringState==='complete'){clearTimeout(timer);resolve();}};
      });
      return new Response(receiver.localDescription.sdp, {status:201,headers:{'Content-Type':'application/sdp',Location:'/media/synthetic/whip/session-1'}});
    }
    if (url.endsWith('/stop')) {
      fixture.stop++;
      if (fixture.failStop) return new Response(JSON.stringify({error:{code:'SERVICE_UNAVAILABLE'}}),{status:503});
      fixture.state='finalizing'; return data(capture());
    }
    if (url.endsWith('/live-captures/synthetic-capture')) {
      if (fixture.state==='finalizing') fixture.state='ready';
      else if(fixture.tracks===2)fixture.state='recording';
      return data(capture());
    }
    return original(input,init);
  };
});

const page = await context.newPage();
const errors=[]; page.on('pageerror',error=>errors.push(error.message));
async function check(name, action) { await action(); result.checks.push(name); console.log('PASS '+name); }
try {
  await page.goto(base+'/capture-demo.html#pairing_token='+'a'.repeat(64));
  await page.waitForSelector('#start');
  await check('existing phone_url enters the built phone page and erases its fragment',async()=>{
    await page.waitForURL('**/capture-phone.html');
    assert.equal(await page.evaluate(()=>location.hash),'');
    assert.equal(await page.evaluate(()=>window.__capturePairing),undefined);
    assert.equal(await page.locator('#start').isEnabled(),true);
    assert.match(await page.locator('#server-state').innerText(),/尚未确认/);
  });
  await check('narrow and desktop layouts have no page overflow and reachable visible focus',async()=>{
    for(const width of [1280,1024,820,390,360]) {
      await page.setViewportSize({width,height:844});
      assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth),true,`overflow at ${width}`);
      assert.ok((await page.locator('#start').boundingBox()).height>=44);
    }
    await page.keyboard.press('Tab');
    assert.equal(await page.evaluate(()=>document.activeElement.id),'start');
    assert.equal(await page.evaluate(()=>getComputedStyle(document.activeElement).outlineStyle),'solid');
    await page.keyboard.press('Tab'); // Disabled stop is skipped; summary is next.
    assert.equal(await page.evaluate(()=>document.activeElement.tagName),'SUMMARY');
    await page.keyboard.press('Enter');assert.equal(await page.locator('details').getAttribute('open'),'');
  });
  await check('real browser fake devices publish audio and video once with actual H264/Opus stats',async()=>{
    await page.locator('#start').click();
    await page.evaluate(()=>document.querySelector('#start').click());
    await page.waitForFunction(()=>document.querySelector('#local-state').textContent.includes('已建立'),null,{timeout:30000});
    await page.waitForFunction(()=>document.querySelector('#diagnostics').textContent.includes('video/h264')&&document.querySelector('#diagnostics').textContent.includes('audio/opus'),null,{timeout:15000});
    const stats=await page.evaluate(()=>JSON.parse(document.querySelector('#diagnostics').textContent));
    result.codecs=stats.outbound_codecs; result.settings=stats.settings;
    assert.equal(await page.evaluate(()=>fixture.exchange),1);assert.equal(await page.evaluate(()=>fixture.publish),1);
    assert.equal(await page.evaluate(()=>document.querySelector('video').muted),true);
    assert.equal(await page.evaluate(()=>document.querySelector('video').srcObject.getTracks().length),2);
    assert.equal(await page.evaluate(()=>document.body.innerText.includes('synthetic-publish')||document.body.innerText.includes('synthetic-control')),false);
    assert.equal(await page.evaluate(()=>localStorage.length+sessionStorage.length),0);
    await page.waitForFunction(()=>document.querySelector('#mic-status').textContent.includes('已检测到输入活动'));
  });
  await check('stop failure releases both devices and supports a control-token retry',async()=>{
    await page.evaluate(()=>{window.testTracks=document.querySelector('video').srcObject.getTracks();fixture.failStop=true;});
    await page.locator('#stop').click();
    await page.waitForFunction(()=>document.querySelector('#stop').textContent==='重试停止');
    assert.equal(await page.evaluate(()=>testTracks.every(track=>track.readyState==='ended')),true);
    assert.equal(await page.evaluate(()=>document.querySelector('video').srcObject),null);
    assert.match(await page.locator('#error').innerText(),/本地已停/);
    await page.evaluate(()=>{fixture.failStop=false;});await page.locator('#stop').click();
    await page.waitForFunction(()=>document.querySelector('#server-state').textContent.includes('正式录像已登记'));
    assert.equal(await page.evaluate(()=>fixture.stop),2);assert.equal(await page.evaluate(()=>fixture.deleted),1);
    assert.equal(await page.locator('#start').isEnabled(),false);
    assert.match(await page.locator('#result').innerText(),/synthetic-media-not-a-formal-id/);
  });
  await check('refresh offers recovery guidance without re-exchanging a pairing',async()=>{
    await page.reload();await page.waitForSelector('#start');
    assert.equal(await page.locator('#start').isEnabled(),false);
    assert.match(await page.locator('#notice').innerText(),/配对信息已丢失/);
    assert.equal(await page.evaluate(()=>fixture.exchange),0);
  });
  await check('offline, hidden page and page departure release devices without a second publisher',async()=>{
    for(const event of ['offline','visibilitychange','pagehide']) {
      await page.goto(base+'/capture-demo.html#pairing_token='+'b'.repeat(64));
      await page.waitForURL('**/capture-phone.html');
      await page.locator('#start').click();
      await page.waitForFunction(()=>document.querySelector('#local-state').textContent.includes('已建立'),null,{timeout:30000});
      await page.evaluate(event=>{
        window.testTracks=document.querySelector('video').srcObject.getTracks();
        if(event==='visibilitychange') {Object.defineProperty(document,'hidden',{configurable:true,value:true});document.dispatchEvent(new Event(event));}
        else window.dispatchEvent(new Event(event));
      },event);
      await page.waitForFunction(()=>testTracks.every(track=>track.readyState==='ended'));
      assert.equal(await page.evaluate(()=>fixture.exchange),1);
      assert.equal(await page.evaluate(()=>fixture.publish),1);
    }
  });
  await page.goto(base+'/capture-phone.html');await page.waitForSelector('#start');
  assert.deepEqual(errors,[]);
  const out=path.resolve('var/phone-capture');await fs.mkdir(out,{recursive:true});
  await page.screenshot({path:path.join(out,'phone-refresh-guidance.png'),fullPage:true});
  await fs.writeFile(path.join(out,'browser-results.json'),JSON.stringify(result,null,2));
  console.log(JSON.stringify(result,null,2));
} catch (error) {
  console.error(await page.evaluate(()=>({local:document.querySelector('#local-state')?.textContent,notice:document.querySelector('#notice')?.textContent,error:document.querySelector('#error')?.textContent,diagnostics:document.querySelector('#diagnostics')?.textContent,exchanges:window.fixture?.exchange,publishes:window.fixture?.publish,pageErrors:[]})));
  throw error;
} finally { await context.close();await browser.close(); }
