package main

// benchPage is the page the scenes run in (scene.go). Its markers (magenta in the top left corner, cyan 24 px in
// from the bottom right one, out of a window's rounded corner) let the framebuffer be matched to it; the square at
// 16 px changes colour on every input it gets, the one at 44 px is green while it has the keyboard. Each scene that
// takes input has a target (a magenta and cyan checker that clicks pass through) where the input goes: the harness
// acts only once it sees it, so nothing lands in another window over the page. It follows /state (the scene, frozen or not) and reports inputs to /event.
const benchPage = `<!doctype html>
<html><head><meta charset="utf-8"><title>vncbench</title>
<style>
html,body{margin:0;height:100%;overflow:hidden;background:#ececec;font:15px/1.45 -apple-system,Helvetica,sans-serif;color:#1d1d1f}
#tl{position:fixed;left:0;top:0;width:12px;height:12px;background:#f0f;z-index:9}
#br{position:fixed;right:24px;bottom:24px;width:16px;height:16px;background:#0ff;z-index:9}
.tg{position:absolute;width:24px;height:24px;pointer-events:none;z-index:8;background:conic-gradient(#0ff 0 25%,#f0f 0 50%,#0ff 0 75%,#f0f 0)}
#mk{position:fixed;left:16px;top:0;width:24px;height:24px;background:rgb(230,30,30);z-index:9}
#fc{position:fixed;left:44px;top:0;width:24px;height:24px;background:rgb(200,40,40);z-index:9}
#bar{position:fixed;left:72px;top:0;right:12px;height:28px;font-size:12px;line-height:28px;color:#666;padding-left:8px}
#stage{position:fixed;left:0;top:28px;right:0;bottom:0;overflow:hidden;background:#fff}
.scene{position:absolute;inset:0;display:none}
.on{display:block}
#idle{padding:24px 32px;box-sizing:border-box}
#idle h1{font-size:26px;margin:0 0 12px}
#idle img,#idle canvas{float:right;width:520px;height:340px;object-fit:cover;margin:0 0 16px 24px;border-radius:8px}
#idle .side{float:left;width:220px;margin-right:24px;color:#444}
#idle .side div{padding:6px 10px;border-radius:6px}
#idle .side div:nth-child(2){background:#e8f0fe}
#type textarea{position:absolute;inset:20px;width:calc(100% - 40px);height:calc(100% - 40px);font:18px/1.5 Menlo,monospace;border:1px solid #ccc;border-radius:8px;padding:16px;box-sizing:border-box;outline:none}
#scroller{position:absolute;inset:0;overflow-y:scroll}
#scroll section{max-width:820px;margin:0 auto;padding:16px 24px}
#scroll h2{margin:8px 0}
#scroll img,#scroll canvas{width:100%;height:380px;object-fit:cover;border-radius:8px;display:block}
#drag{background:linear-gradient(135deg,#dfe7f3,#f6efe6)}
#panel{position:absolute;left:60px;top:60px;width:640px;height:420px;background:#fff;border-radius:10px;box-shadow:0 12px 40px rgba(0,0,0,.25);overflow:hidden}
#panel .head{position:relative;height:28px;background:#e9e9ec;border-bottom:1px solid #d0d0d4;font-size:13px;line-height:28px;text-align:center;cursor:grab}
#panel img,#panel canvas{width:300px;height:200px;object-fit:cover;float:left;margin:12px;border-radius:6px}
#panel p{margin:12px;font-size:14px}
#video{background:#000}
#video canvas{position:absolute;left:50%;top:50%;transform:translate(-50%,-50%)}
</style></head><body>
<div id="tl"></div><div id="br"></div><div id="mk"></div><div id="fc"></div><div id="bar">vncbench</div>
<div id="stage">
 <div class="scene" id="idle"><h1>Quarterly notes</h1><div class="side"><div>Inbox</div><div>Drafts</div><div>Sent</div><div>Archive</div><div>Projects</div><div>Calendar</div></div><span class="ph"></span><div class="txt"></div></div>
 <div class="scene" id="type"><textarea spellcheck="false" placeholder="Type here"></textarea><div class="tg" style="left:48px;top:48px"></div></div>
 <div class="scene" id="scroll"><div id="scroller"></div><div class="tg" style="left:48px;top:48px;position:fixed;margin-top:28px"></div></div>
 <div class="scene" id="drag"><div id="panel"><div class="head">Preview — drag me<div class="tg" style="left:12px;top:2px"></div></div><span class="ph"></span><div class="txt"></div></div></div>
 <div class="scene" id="video"><canvas width="960" height="540"></canvas></div>
</div>
<script>
const PAL=['rgb(230,30,30)','rgb(30,200,30)','rgb(30,30,230)','rgb(230,200,30)','rgb(20,20,20)','rgb(240,240,240)','rgb(240,120,20)','rgb(150,30,200)'];
let n=0, scene='', frozen=false, photos=0;
const mk=document.getElementById('mk'), fc=document.getElementById('fc'), bar=document.getElementById('bar');
function bump(){ n++; mk.style.background=PAL[n%8]; }
function report(k,e){ const t=performance.timeOrigin+e.timeStamp;
  requestAnimationFrame(ts=>{ const p=performance.timeOrigin+ts;
    fetch('/event',{method:'POST',body:JSON.stringify({k,t,p})}).catch(()=>{}); }); }
const words='lorem ipsum dolor sit amet consectetur adipiscing elit sed do eiusmod tempor incididunt ut labore et dolore magna aliqua enim ad minim veniam quis nostrud exercitation ullamco laboris nisi aliquip ex ea commodo consequat duis aute irure in reprehenderit voluptate velit esse cillum fugiat nulla pariatur'.split(' ');
let seed=7; function rnd(){ seed=(seed*1103515245+12345)&0x7fffffff; return seed/0x7fffffff; }
function para(k){ let s=''; for(let i=0;i<k;i++) s+=words[Math.floor(rnd()*words.length)]+(rnd()<.08?'. ':' '); return '<p>'+s+'</p>'; }
// A photograph-like texture when there are no photos: value noise in a few octaves, coloured.
function texture(w,h,s){ const c=document.createElement('canvas'); c.width=w; c.height=h; const g=c.getContext('2d'); const d=g.createImageData(w,h);
  const grid=[]; for(let o=0;o<5;o++){ const k=4<<o, a=[]; for(let i=0;i<(k+1)*(k+1);i++) a.push(rnd()); grid.push([k,a]); }
  for(let y=0;y<h;y++) for(let x=0;x<w;x++){ let v=0, amp=.5; for(const [k,a] of grid){ const fx=x/w*k, fy=y/h*k, ix=Math.floor(fx), iy=Math.floor(fy), tx=fx-ix, ty=fy-iy;
      const q=(i,j)=>a[(iy+j)*(k+1)+ix+i]; v+=amp*((q(0,0)*(1-tx)+q(1,0)*tx)*(1-ty)+(q(0,1)*(1-tx)+q(1,1)*tx)*ty); amp/=2; }
    const i=(y*w+x)*4; d.data[i]=40+v*180+s*20; d.data[i+1]=70+v*150; d.data[i+2]=60+(1-v)*160-s*10; d.data[i+3]=255; }
  g.putImageData(d,0,0); return c; }
function picture(i,w,h){ if(photos>0){ const im=new Image(); im.src='/photo/'+(i%photos); return im; } return texture(w,h,i%5); }
function build(){
  document.querySelectorAll('.txt').forEach(e=>{ let s=''; for(let i=0;i<6;i++) s+=para(40); e.innerHTML=s; });
  document.querySelectorAll('.ph').forEach((e,i)=>e.replaceWith(picture(i,520,340)));
  const sc=document.getElementById('scroller'); for(let i=0;i<30;i++){ const s=document.createElement('section'); s.innerHTML='<h2>Section '+(i+1)+'</h2>'+para(60)+para(50);
    s.appendChild(picture(i+2,820,380)); s.insertAdjacentHTML('beforeend',para(70)); sc.appendChild(s); }
}
function show(s){ if(s===scene) return; scene=s; document.querySelectorAll('.scene').forEach(e=>e.classList.toggle('on',e.id===s)); bar.textContent='vncbench · '+s; }
setInterval(()=>{ fc.style.background=document.hasFocus()&&document.activeElement&&document.activeElement.tagName==='TEXTAREA'?'rgb(30,200,30)':'rgb(200,40,40)'; },100);
async function poll(){ try{ const r=await fetch('/state'); const s=await r.json(); if(photos!==s.photos && !document.querySelector('section')){ photos=s.photos; build(); } show(s.scene); frozen=s.freeze; }catch(e){} setTimeout(poll,100); }
document.addEventListener('keydown',e=>{ bump(); report('key',e); });
document.getElementById('scroller').addEventListener('wheel',e=>report('wheel',e),{passive:true});
document.getElementById('scroller').addEventListener('scroll',()=>bump(),{passive:true});
const panel=document.getElementById('panel'); let drag=null;
panel.querySelector('.head').addEventListener('pointerdown',e=>{ drag={x:e.clientX-panel.offsetLeft,y:e.clientY-panel.offsetTop}; report('down',e); });
document.addEventListener('pointermove',e=>{ if(!drag) return; panel.style.left=(e.clientX-drag.x)+'px'; panel.style.top=(e.clientY-drag.y)+'px'; bump(); report('move',e); });
document.addEventListener('pointerup',()=>{ drag=null; });
// The video: a photograph panned and zoomed, things moving over it and a little grain, 30 times a second.
const vc=document.querySelector('#video canvas'), vg=vc.getContext('2d'); let src=null, t=0, last=0, frames=[];
function frame(ts){ requestAnimationFrame(frame); if(scene!=='video'||frozen) return; if(ts-last<32) return; last=ts; t++;
  if(!src){ src=texture(1280,720,3); }
  const z=1.15+0.1*Math.sin(t/90), w=960*z, h=540*z; vg.drawImage(src,-(w-960)/2+60*Math.sin(t/70),-(h-540)/2+40*Math.cos(t/55),w,h);
  for(let i=0;i<6;i++){ vg.fillStyle='hsla('+(i*60+t)%360+',70%,55%,.8)'; vg.beginPath(); vg.arc(480+300*Math.sin(t/40+i),270+180*Math.cos(t/33+i*2),40+10*i,0,7); vg.fill(); }
  const d=vg.getImageData(0,0,960,540); for(let i=0;i<d.data.length;i+=4){ const g=(rnd()-.5)*18; d.data[i]+=g; d.data[i+1]+=g; d.data[i+2]+=g; } vg.putImageData(d,0,0);
  vg.fillStyle='#fff'; vg.font='28px -apple-system'; vg.fillText('frame '+t,24,48);
  frames.push(ts); while(frames.length&&ts-frames[0]>1000) frames.shift(); }
requestAnimationFrame(frame);
fetch('/event',{method:'POST',body:JSON.stringify({k:'hello',w:innerWidth,h:innerHeight,dpr:devicePixelRatio})});
poll();
</script></body></html>
`
