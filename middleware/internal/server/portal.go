// Package server 中的 portal 提供 App 套壳 WebView 加载的用户功能页面：
// 设备信息、文件浏览/上传，以及通过 JS 桥接原生 ExoPlayer 播放与 SAF 备份。
// 与 /admin 配置控制台共用同一 cookie 会话，App 端无需存 token。
package server

import (
	"html/template"
	"net/http"
)

// portalTmpl 极简内联模板，仅注入主机名用于展示；其余逻辑由前端 JS 完成。
const portalSrc = `<!doctype html>
<html lang="zh-CN"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover">
<meta name="theme-color" content="#0f1115">
<title>DDNAS</title>
<style>
:root{--bg:#0f1115;--card:#1a1d24;--fg:#e6e6e6;--muted:#8a8f99;--accent:#4f9cff;--ok:#3ecf8e;--warn:#f5a623;--err:#ef5b5b;--bd:#2a2e37}
*{box-sizing:border-box;-webkit-tap-highlight-color:transparent}
html,body{margin:0;height:100%}
body{font-family:system-ui,Segoe UI,Roboto,sans-serif;background:var(--bg);color:var(--fg);overscroll-behavior:none}
header{position:sticky;top:0;z-index:5;display:flex;align-items:center;justify-content:space-between;padding:12px 14px;background:var(--card);border-bottom:1px solid var(--bd);padding-top:max(12px,env(safe-area-inset-top))}
header .brand{font-weight:700;letter-spacing:.5px}
header .host{color:var(--muted);font-size:12px;margin-left:8px}
.tabs{display:flex;gap:8px;padding:10px 14px;border-bottom:1px solid var(--bd);background:var(--bg);position:sticky;top:48px}
.tabs button{flex:1;background:transparent;border:1px solid var(--bd);color:var(--muted);border-radius:10px;padding:9px;font-size:14px}
.tabs button.on{background:var(--accent);color:#fff;border-color:var(--accent)}
.view{padding:14px}
.card{background:var(--card);border:1px solid var(--bd);border-radius:12px;padding:14px;margin-bottom:12px}
.card h3{margin:0 0 10px;font-size:15px}
.bar{height:8px;border-radius:6px;background:#0c0e12;overflow:hidden;margin:4px 0 8px}
.bar>i{display:block;height:100%;background:linear-gradient(90deg,var(--accent),var(--ok))}
.kv{display:flex;justify-content:space-between;gap:8px;padding:5px 0;font-size:13px;border-bottom:1px dashed var(--bd)}
.kv:last-child{border-bottom:0}
.kv .k{color:var(--muted)}
.mono{font-family:ui-monospace,SFMono-Regular,Menlo,monospace}
.row{display:flex;align-items:center;gap:10px}
.fab{position:fixed;right:16px;bottom:max(20px,env(safe-area-inset-bottom));z-index:6;width:52px;height:52px;border-radius:50%;border:0;background:var(--accent);color:#fff;font-size:22px;box-shadow:0 6px 18px rgba(0,0,0,.5)}
.crumb{display:flex;flex-wrap:wrap;gap:4px;align-items:center;padding:8px 0;font-size:13px;color:var(--muted)}
.crumb a{color:var(--accent)}
.list{display:flex;flex-direction:column;gap:8px}
.item{display:flex;align-items:center;gap:10px;padding:11px 12px;border:1px solid var(--bd);border-radius:10px;background:#0c0e12}
.item:active{background:#11141a}
.item .ic{font-size:18px;width:22px;text-align:center}
.item .nm{flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:14px}
.item .mt{color:var(--muted);font-size:12px;white-space:nowrap}
.item button{border:1px solid var(--bd);background:transparent;color:var(--accent);border-radius:8px;padding:6px 10px;font-size:13px}
.empty,.loading,.err{padding:24px;text-align:center;color:var(--muted)}
.err{color:var(--err)}
.toast{position:fixed;left:50%;bottom:30px;transform:translateX(-50%);background:var(--card);border:1px solid var(--bd);color:var(--fg);padding:8px 14px;border-radius:10px;font-size:13px;z-index:9;opacity:0;transition:opacity .2s}
.toast.on{opacity:1}
</style>
</head><body>
<header><div class="row"><span class="brand">DDNAS</span><span class="host">{{.Host}}</span></div>
<div class="row"><a style="font-size:13px;color:var(--muted)" href="/admin/">← 控制台</a>
<button class="fab" title="备份" onclick="ddnas.startBackup()">&#128190;</button></div></header>
<nav class="tabs"><button id="tab-dev" class="on" onclick="showTab('dev')">设备</button><button id="tab-files" onclick="showTab('files')">文件</button></nav>

<section id="view-dev" class="view">
  <div id="dev-body" class="loading">加载中…</div>
</section>
<section id="view-files" class="view" hidden>
  <div class="crumb" id="crumb"></div>
  <div style="display:flex;gap:8px;margin-bottom:10px">
    <button class="fab" style="position:static;width:auto;height:auto;border-radius:8px;padding:8px 12px;font-size:14px" onclick="goUp()">↑ 上级</button>
    <label class="fab" style="position:static;width:auto;height:auto;border-radius:8px;padding:8px 12px;font-size:14px;cursor:pointer">+ 上传<input type="file" id="upfile" hidden onchange="upload(this)"></label>
  </div>
  <div id="files-body" class="loading">加载中…</div>
</section>

<div id="toast" class="toast"></div>

<script>
const MEDIA=/(mp4|mkv|mov|avi|webm|m4v|mp3|flac|aac|m4a|wav|ts)$/i;
let cur=""; // 当前目录相对路径（无 root 前缀），"" 表示根
let tab="dev";
function showTab(t){tab=t;document.getElementById("tab-dev").classList.toggle("on",t==="dev");document.getElementById("tab-files").classList.toggle("on",t==="files");document.getElementById("view-dev").hidden=t!=="dev";document.getElementById("view-files").hidden=t!=="files";if(t==="dev"&&!devLoaded)loadDev();if(t==="files"&&!filesLoaded)loadFiles("");}
async function loadDev(){
  const b=document.getElementById("dev-body");b.className="loading";b.textContent="加载中…";
  try{
    const r=await fetch("/portal/api/node/system");if(!r.ok)throw new Error("HTTP "+r.status);
    const s=await r.json();devLoaded=true;renderDev(s);
  }catch(e){b.className="err";b.textContent="设备信息适配器未启用或获取失败："+e.message;}
}
function pct(v){return (v||0).toFixed(1)+"%";}
function fmtBytes(v){v=v||0;const u=["B","KB","MB","GB","TB"];let i=0;while(v>=1024&&i<u.length-1){v/=1024;i++;}return v.toFixed(v>=100?0:1)+" "+u[i];}
function uptime(s){s=s||0;const d=Math.floor(s/86400),h=Math.floor(s%86400/3600),m=Math.floor(s%3600/60);return(d?d+"天":"")+(h?h+"时":"")+(m?m+"分":"");}
function renderDev(s){
  const b=document.getElementById("dev-body");
  let h="";
  h+=card("概览",kv("主机",esc(s.hostname))+"<br>"+kv("系统",esc(s.os))+"<br>"+kv("内核",esc(s.kernel))+"<br>"+kv("架构",esc(s.arch))+"<br>"+kv("运行时长",uptime(s.uptime_seconds)));
  if(s.cpu)h+=card("CPU",kv("核心",s.cpu.cores)+"<br>"+kv("负载1/5/15",+(s.cpu.load1||0).toFixed(2)+" / "+(s.cpu.load5||0).toFixed(2)+" / "+(s.cpu.load15||0).toFixed(2))+bar(s.cpu.usage_percent)+"<br>"+kv("使用率",pct(s.cpu.usage_percent)));
  if(s.memory)h+=card("内存",kv("已用/总",fmtBytes(s.memory.used_bytes)+" / "+fmtBytes(s.memory.total_bytes))+bar(s.memory.usage_percent)+"<br>"+kv("使用率",pct(s.memory.usage_percent)));
  if(s.disks&&s.disks.length)h+=card("磁盘",s.disks.map(d=>kv("挂载",esc(d.mountpoint)+" ("+esc(d.fstype)+")")+"<br>"+kv("已用/总",fmtBytes(d.used_bytes)+" / "+fmtBytes(d.total_bytes))+bar(d.usage_percent)+"<br>"+kv("使用率",pct(d.usage_percent))).join("<hr style='border-color:var(--bd)'>"));
  if(s.network&&s.network.length)h+=card("网络",s.network.map(n=>kv("网卡",esc(n.device))+"<br>"+kv("接收/发送",fmtBytes(n.rx_bytes)+" / "+fmtBytes(n.tx_bytes)).join("<hr style='border-color:var(--bd)'>"));
  if(!h)h="<div class='empty'>无设备信息</div>";
  b.className="";b.innerHTML=h;
}
function card(t,c){return '<div class="card"><h3>'+esc(t)+'</h3>'+c+'</div>';}
function kv(k,v){return '<div class="kv"><span class="k">'+esc(k)+'</span><span>'+v+'</span></div>';}
function bar(v){v=v||0;return '<div class="bar"><i style="width:'+Math.min(100,v).toFixed(1)+'%"></i></div>';}
let devLoaded=false,filesLoaded=false;
async function loadFiles(p){
  cur=p;const b=document.getElementById("files-body");b.className="loading";b.textContent="加载中…";
  renderCrumb(p);
  try{
    const r=await fetch("/portal/api/openlist/files/list?path="+encodeURIComponent(p));if(!r.ok)throw new Error("HTTP "+r.status);
    const s=await r.json();filesLoaded=true;
    const items=(s.items||[]).slice().sort((a,b)=>(b.is_dir?1:0)-(a.is_dir?1:0)||String(a.name).localeCompare(String(b.name)));
    if(!items.length){b.className="empty";b.textContent="空目录";return;}
    b.className="";b.innerHTML='<div class="list">'+items.map(it=>{
      const name=esc(it.name),dir=joinPath(p,it.name);
      if(it.is_dir){return '<div class="item" onclick="loadFiles(\''+escJS(dir)+'\')"><span class="ic">&#128193;</span><span class="nm">'+name+'</span></div>';}
      const ext=(it.name.split('.').pop()||"").toLowerCase();
      if(MEDIA.test(ext)){return '<div class="item"><span class="ic">&#127916;</span><span class="nm">'+name+'</span><span class="mt">'+fmtBytes(it.size)+'</span><button onclick="play(\''+escJS(dir)+'\')">播放</button></div>';}
      return '<div class="item"><span class="ic">&#128196;</span><span class="nm">'+name+'</span><span class="mt">'+fmtBytes(it.size)+'</span></div>';
    }).join("")+'</div>';
  }catch(e){b.className="err";b.textContent="文件适配器未启用或获取失败："+e.message;}
}
function joinPath(base,name){return base?base+"/"+name:name;}
function goUp(){if(!cur)return;const i=cur.lastIndexOf("/");loadFiles(i<0?"":cur.slice(0,i));}
function renderCrumb(p){
  const c=document.getElementById("crumb");let h='<a onclick="loadFiles(\'\')">根</a>';
  if(p){const segs=p.split("/");let acc="";segs.forEach((s,i)=>{acc=acc?acc+"/"+s:s;h+='<span>/</span><a onclick="loadFiles(\''+escJS(acc)+'\')">'+esc(s)+'</a>';});}
  c.innerHTML=h;
}
function play(relPath){
  // 逐段 encodeURIComponent，保留 "/" 作为路径分隔，Go {path...} 会正确捕获。
  const url=location.origin+"/portal/api/openlist/files/stream/"+relPath.split("/").map(encodeURIComponent).join("/");
  ddnas.playMedia(url);
}
async function upload(input){
  const f=input.files[0];if(!f)return;
  const dest=joinPath(cur,f.name);toast("上传中…");
  try{
    const r=await fetch("/portal/api/openlist/files/upload?path="+encodeURIComponent(dest),{method:"POST",body:f,headers:{"Content-Type":"application/octet-stream"}});
    if(!r.ok)throw new Error("HTTP "+r.status);
    toast("上传完成");loadFiles(cur);
  }catch(e){toast("上传失败："+e.message);}
  input.value="";
}
function toast(m){const t=document.getElementById("toast");t.textContent=m;t.classList.add("on");setTimeout(()=>t.classList.remove("on"),1800);}
function esc(s){return String(s==null?"":s).replace(/[&<>"]/g,c=>({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;"}[c]));}
function escJS(s){return String(s==null?"":s).replace(/\\/g,"\\\\").replace(/'/g,"\\'");}
if(typeof ddnas==="undefined"){window.ddnas={playMedia:u=>alert("原生桥不可用，播放:\n"+u),startBackup:()=>alert("原生桥不可用，备份需 App 内启动")};}
(function(){
  const q=new URLSearchParams(location.search);const t=q.get("tab");showTab(t==="files"?"files":"dev");
})();
</script>
</body></html>`

var portalTmpl = template.Must(template.New("portal").Parse(portalSrc))

// servePortal 渲染 App 套壳加载的 /portal 页面；未登录跳转到 /admin/login。
func (s *Server) servePortal(w http.ResponseWriter, r *http.Request) {
	if !s.admin.LoggedIn(r) {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	host := r.Host
	if host == "" {
		host = "localhost"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = portalTmpl.Execute(w, map[string]string{"Host": host})
}
