// Package server 中的 portal 提供 App 套壳 WebView 加载的用户功能页面。
// 采用 App 壳子 + JS 桥 的方案：UI 全部在 Docker Web 端实现，App 端仅通过
// ddnas.playMedia(url) / ddnas.startBackup() 两个原生入口桥接播放和备份。
// 所有数据请求走同源 /portal/api/*，由 Go 后端反代到内网适配器
// （node_exporter / AList / ...），客户端永远只访问中间件 :8080，无跨域。
package server

import (
	"net/http"
)

// portalSrc 内联完整 SPA。仿照用户参考的极空间 App 视觉：
//   - 顶部：搜索条 + 用户信息（可选），首页渲染 NAS 卡片（型号、存储用量进度条、设备图标）
//   - 中部：12 格彩色圆角图标宫格（云盘/文件管理/相册/影视/备份/下载/任务中心/回收站...）
//   - 下方：CPU/内存/网络/硬盘 4 个监控小卡（半环形 or 数字+进度条），10s 轮询刷新
//   - 文件页：面包屑、返回上级、文件列表（目录/文件图标 + 大小/修改时间 + 播放按钮）、上传 + 刷新
//   - 我的页：当前中间件信息 + 备份入口（调 JS 桥） + 控制台入口
//   - 底部：三栏导航（首页 / 文件 / 我的）
const portalSrc = `<!doctype html>
<html lang="zh-CN"><head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1,viewport-fit=cover,user-scalable=no">
<meta name="theme-color" content="#f3f5fa">
<title>DDNAS</title>
<style>
:root{
  --bg:#f3f5fa;
  --card:#ffffff;
  --fg:#1a1d29;
  --muted:#8a94a6;
  --muted2:#b7bfcc;
  --accent:#3478f6;
  --ok:#25c275;
  --warn:#f5a623;
  --err:#ef5b5b;
  --bd:#eaeef5;
  --surface2:#f6f8fc;
  --chip:#eef2fa;
}
@media (prefers-color-scheme: dark){
  :root{
    --bg:#0e1018;--card:#171a25;--fg:#edf0f6;--muted:#8e95a7;--muted2:#5b6375;
    --bd:#252a39;--surface2:#12151f;--chip:#1a2030;
  }
}
*{box-sizing:border-box;-webkit-tap-highlight-color:transparent}
html,body{margin:0;min-height:100%}
body{font-family:system-ui,-apple-system,BlinkMacSystemFont,"Helvetica Neue",Arial,sans-serif;background:var(--bg);color:var(--fg);overscroll-behavior:none;font-size:14px;line-height:1.5;
  padding-top:calc(env(safe-area-inset-top) + 4px);
  /* App WebView 不传 env(safe-area-inset-bottom)，用固定大值覆盖 tabbar(56px)+系统导航栏(48px)+余量 */
  padding-bottom:calc(env(safe-area-inset-bottom) + 112px);
}
a{color:var(--accent);text-decoration:none}
button{border:0;background:transparent;color:inherit;font:inherit;padding:0;cursor:pointer}

/* ===== 顶栏（仅首页、文件页显示搜索/返回，我的页隐藏） ===== */
.topbar{
  position:sticky;top:0;z-index:10;background:var(--bg);
  padding:10px 14px 12px;border-bottom:1px solid transparent;
}
.search{
  display:flex;align-items:center;gap:8px;
  background:var(--surface2);border-radius:999px;padding:9px 14px;
  color:var(--muted);font-size:13px;border:1px solid var(--bd);
}
.search .ic{font-size:15px}
.top-actions{display:flex;gap:10px;margin-top:10px;align-items:center;justify-content:flex-end}
.icon-btn{width:34px;height:34px;border-radius:10px;display:inline-flex;align-items:center;justify-content:center;background:var(--surface2);border:1px solid var(--bd);font-size:15px}

.page{padding:4px 14px 12px}

/* ===== NAS 卡片（参考极空间：头像 / 设备名 / 存储用量条 / 设备图） ===== */
.nas-card{
  background:linear-gradient(135deg,#3478f6 0%,#5a93ff 100%);
  color:#fff;border-radius:18px;padding:16px 18px;margin-bottom:14px;position:relative;overflow:hidden;
}
@media (prefers-color-scheme: dark){
  .nas-card{background:linear-gradient(135deg,#285dd1 0%,#4075e0 100%)}
}
.nas-card::after{
  content:"";position:absolute;right:-30px;top:-30px;width:160px;height:160px;border-radius:50%;background:rgba(255,255,255,.08);
}
.nas-user{display:flex;align-items:center;gap:10px;margin-bottom:10px;position:relative;z-index:1}
.avatar{width:34px;height:34px;border-radius:50%;background:rgba(255,255,255,.25);display:inline-flex;align-items:center;justify-content:center;font-weight:700;font-size:14px}
.nas-user .meta{flex:1;min-width:0}
.nas-user .name{font-weight:600;font-size:14px}
.nas-user .role{font-size:11px;opacity:.8;margin-top:2px}
.nas-title{display:flex;align-items:flex-start;justify-content:space-between;position:relative;z-index:1;gap:10px}
.nas-title .txt{flex:1;min-width:0}
.nas-title .mod{font-size:20px;font-weight:700;letter-spacing:.5px}
.nas-title .tag{display:inline-block;margin-left:8px;background:rgba(255,255,255,.2);font-size:11px;padding:2px 8px;border-radius:999px;vertical-align:middle}
.nas-usage{margin-top:12px;position:relative;z-index:1}
.usage-bar{height:8px;background:rgba(255,255,255,.22);border-radius:999px;overflow:hidden}
.usage-bar>i{display:block;height:100%;background:#fff;width:0;transition:width .5s}
.usage-meta{display:flex;justify-content:space-between;margin-top:6px;font-size:12px;opacity:.92}
.device-art{font-size:42px;line-height:1}

/* ===== 功能宫格：4 列 × 3 行 圆角图标 + 文字 ===== */
.grid{
  background:var(--card);border:1px solid var(--bd);border-radius:18px;
  padding:14px 6px 6px;margin-bottom:14px;
  display:grid;grid-template-columns:repeat(4,1fr);gap:4px 0;
}
.cell{display:flex;flex-direction:column;align-items:center;justify-content:flex-start;padding:8px 4px 12px;user-select:none}
.cell:active{opacity:.6}
.cell .icon{
  width:46px;height:46px;border-radius:14px;
  display:inline-flex;align-items:center;justify-content:center;
  font-size:22px;margin-bottom:6px;color:#fff;
}
.cell .label{font-size:12px;color:var(--fg)}
.cell.disabled .label{color:var(--muted2)}
.cell.disabled .icon{background:#d7dbe5 !important}
/* 12 种配色 */
.c1{background:linear-gradient(135deg,#5aa4ff,#3478f6)}
.c2{background:linear-gradient(135deg,#38c77a,#1aa35f)}
.c3{background:linear-gradient(135deg,#ff8c42,#ef6a2b)}
.c4{background:linear-gradient(135deg,#8c6bff,#6647e6)}
.c5{background:linear-gradient(135deg,#ff5c7e,#ef3a63)}
.c6{background:linear-gradient(135deg,#4cc9d4,#2da9b5)}
.c7{background:linear-gradient(135deg,#f5c34a,#d99e1b)}
.c8{background:linear-gradient(135deg,#6aa9ff,#4a83e0)}
.c9{background:linear-gradient(135deg,#7e8bff,#5c6be6)}
.c10{background:linear-gradient(135deg,#ff9c6a,#e07b4a)}
.c11{background:linear-gradient(135deg,#43c5a2,#25a082)}
.c12{background:linear-gradient(135deg,#a0a6b3,#7c8392)}

/* ===== 监控 4 卡（2×2）：半环 + 指标 ===== */
/* 监控卡：纵向堆叠的横向长方块，每行一项，紧凑 */
.monitor{display:flex;flex-direction:column;gap:8px;margin-bottom:14px}
.m-card{background:var(--card);border:1px solid var(--bd);border-radius:12px;padding:10px 14px}
.m-card .m-hd{display:flex;align-items:center;justify-content:space-between;gap:8px}
.m-card .m-title{display:flex;align-items:center;gap:6px;color:var(--muted);font-size:12px;font-weight:600}
.m-card .m-title .dot{width:6px;height:6px;border-radius:50%;background:var(--ok)}
.m-card .m-title.err .dot{background:var(--warn)}
.m-card .m-right{font-size:14px;font-weight:700}
.m-card .m-bar{height:5px;background:var(--surface2);border-radius:999px;overflow:hidden;margin-top:8px}
.m-card .m-bar>i{display:block;height:100%;width:0;background:var(--accent);transition:width .5s}
.m-card .m-bar.ok>i{background:var(--ok)}
.m-card .m-bar.warn>i{background:var(--warn)}
.m-card .m-bar.err>i{background:var(--err)}
.m-card .m-vals{display:flex;gap:14px;margin-top:6px;font-size:11px;color:var(--muted);flex-wrap:wrap}
.m-card .m-vals b{color:var(--fg);font-weight:600}

/* 错误 / 未启用提示卡 */
.tip-card{
  background:var(--card);border:1px dashed var(--bd);border-radius:14px;padding:14px;
  color:var(--muted);font-size:13px;margin-bottom:14px;text-align:center;
}

/* ===== 文件页 ===== */
.file-bar{
  position:sticky;top:0;z-index:9;background:var(--bg);padding:10px 14px 12px;
  display:flex;flex-direction:column;gap:8px;border-bottom:1px solid var(--bd);
}
.file-top{display:flex;align-items:center;gap:10px}
.file-top .back{width:34px;height:34px;border-radius:10px;background:var(--surface2);border:1px solid var(--bd);display:inline-flex;align-items:center;justify-content:center;font-size:16px}
.file-top .path{flex:1;min-width:0;font-weight:600;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.file-top .up{background:var(--accent);color:#fff;border-radius:10px;padding:8px 12px;font-size:13px;display:inline-flex;align-items:center;gap:6px}
.crumb{display:flex;flex-wrap:wrap;gap:2px 4px;color:var(--muted);font-size:12px;overflow-x:auto}
.crumb a{color:var(--accent);white-space:nowrap}
.crumb .sep{color:var(--muted2)}
#files-body{padding:4px 14px 12px}
.flist{display:flex;flex-direction:column;gap:6px}
.fitem{
  display:flex;align-items:center;gap:12px;padding:11px 12px;
  background:var(--card);border:1px solid var(--bd);border-radius:14px;
}
.fitem:active{background:var(--surface2)}
.fic{width:40px;height:40px;border-radius:10px;display:inline-flex;align-items:center;justify-content:center;font-size:20px;background:var(--chip);flex-shrink:0}
.fic.dir{background:linear-gradient(135deg,#ffd783,#f5a623);color:#fff}
.fic.video{background:linear-gradient(135deg,#a06bff,#6f46e6);color:#fff}
.fic.audio{background:linear-gradient(135deg,#4cc9d4,#2da9b5);color:#fff}
.fic.image{background:linear-gradient(135deg,#ff83a6,#ef4a75);color:#fff}
.fic.doc{background:linear-gradient(135deg,#5aa4ff,#3478f6);color:#fff}
.fn{flex:1;min-width:0}
.fn .nm{font-size:14px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.fn .mt{font-size:11px;color:var(--muted);margin-top:2px}
.fbtn{color:var(--accent);font-size:12px;padding:6px 10px;border-radius:8px;background:var(--chip)}
.empty,.loading,.err{padding:32px 16px;text-align:center;color:var(--muted)}
.err{color:var(--err)}

/* ===== 我的页 ===== */
.me{padding:10px 14px 16px;display:flex;flex-direction:column;gap:12px}
.me-head{
  display:flex;align-items:center;gap:12px;
  background:linear-gradient(135deg,#3478f6,#5a93ff);color:#fff;border-radius:18px;padding:16px;
}
@media (prefers-color-scheme: dark){
  .me-head{background:linear-gradient(135deg,#285dd1,#4075e0)}
}
.me-head .av{width:54px;height:54px;border-radius:50%;background:rgba(255,255,255,.22);display:inline-flex;align-items:center;justify-content:center;font-weight:700;font-size:20px}
.me-head .t{font-size:16px;font-weight:600}
.me-head .s{font-size:12px;opacity:.85;margin-top:2px}
.section{background:var(--card);border:1px solid var(--bd);border-radius:16px;overflow:hidden}
.sitem{display:flex;align-items:center;gap:12px;padding:14px;position:relative}
.sitem+.sitem::before{content:"";position:absolute;left:52px;right:0;top:0;border-top:1px solid var(--bd)}
.sitem .ic{width:32px;height:32px;border-radius:10px;background:var(--chip);display:inline-flex;align-items:center;justify-content:center;font-size:16px;flex-shrink:0}
.sitem .lbl{flex:1;min-width:0;font-size:14px}
.sitem .desc{font-size:12px;color:var(--muted);margin-top:2px}
.sitem .arr{color:var(--muted2);font-size:16px}
.sitem.big .ic{background:linear-gradient(135deg,#38c77a,#1aa35f);color:#fff}
.sitem.big .lbl{font-weight:600}

/* 备份设置面板：行布局，标签+值+动作按钮 */
.bk-panel{
  padding:6px 14px 14px;display:flex;flex-direction:column;gap:10px;
  border-top:1px solid var(--bd);margin-top:4px;background:var(--surface2);
}
.bk-row{display:flex;align-items:center;gap:8px;min-height:36px}
.bk-k{font-size:12px;color:var(--muted);width:64px;flex-shrink:0}
.bk-v{flex:1;min-width:0;font-size:13px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.bk-v.empty{color:var(--muted2)}
.bk-input{
  flex:1;min-width:0;font:inherit;font-size:13px;color:var(--fg);
  background:var(--card);border:1px solid var(--bd);border-radius:8px;padding:8px 10px;
}
.bk-input:focus{outline:none;border-color:var(--accent)}
.bk-btn{
  flex-shrink:0;background:var(--chip);color:var(--accent);
  font-size:12px;border-radius:8px;padding:7px 10px;border:1px solid var(--bd);
}
.bk-btn:active{opacity:.6}
.bk-row.warn .bk-v{color:var(--warn)}

/* ===== 底部三栏导航 ===== */
.tabbar{
  position:fixed;left:0;right:0;bottom:0;z-index:20;
  background:var(--card);border-top:1px solid var(--bd);
  display:grid;grid-template-columns:repeat(3,1fr);
  padding-bottom:env(safe-area-inset-bottom);
}
.tabbar button{
  display:flex;flex-direction:column;align-items:center;gap:3px;
  padding:8px 0 6px;color:var(--muted2);
}
.tabbar button .ic{font-size:22px;line-height:1}
.tabbar button .lb{font-size:11px}
.tabbar button.on{color:var(--accent)}

/* ===== 通用 ===== */
.hidden{display:none !important}
.toast{position:fixed;left:50%;bottom:100px;transform:translateX(-50%);background:rgba(0,0,0,.8);color:#fff;padding:8px 14px;border-radius:10px;font-size:13px;z-index:99;opacity:0;transition:opacity .2s;pointer-events:none}
.toast.on{opacity:1}
.spin{display:inline-block;width:14px;height:14px;border:2px solid var(--muted2);border-top-color:transparent;border-radius:50%;animation:spin .8s linear infinite;vertical-align:middle;margin-right:6px}
@keyframes spin{to{transform:rotate(360deg)}}
</style>
</head>
<body>

<!-- ========== 首页 ========== -->
<section id="view-home">
  <div class="topbar">
    <div class="search" onclick="toast('搜索后续扩展')"><span class="ic">🔎</span>搜索设备、文件、相册…</div>
  </div>
  <div class="page">

    <div class="nas-card">
      <div class="nas-title" style="flex-direction:column;align-items:stretch;gap:6px">
        <div class="txt" style="display:flex;align-items:center;flex-wrap:wrap;gap:8px">
          <span class="mod" id="d-model">DDNAS</span><span class="tag" id="d-net">内网</span>
        </div>
        <div style="display:flex;align-items:center;gap:8px;width:100%">
          <div style="flex:1;min-width:0;font-size:12px;opacity:.85;white-space:nowrap;overflow:hidden;text-overflow:ellipsis" id="d-desc">家庭私有云 · 中间件 v1</div>
          <span style="font-size:11px;opacity:.85;white-space:nowrap;text-align:right;min-width:96px;max-width:140px;overflow:hidden;text-overflow:ellipsis;flex-shrink:0" id="d-stat">初始化中…</span>
          <button id="d-refresh" style="font-size:13px;color:#fff;opacity:.85;background:rgba(255,255,255,.18);border-radius:999px;width:26px;height:26px;display:none;align-items:center;justify-content:center;flex-shrink:0" onclick="forceRefreshSystem()">↻</button>
        </div>
      </div>
      <div class="nas-usage">
        <div class="usage-bar"><i id="d-usage-bar"></i></div>
        <div class="usage-meta">
          <span>已使用 <b id="d-used">0</b></span>
          <span><b id="d-total">0</b> 可用容量 <b id="d-free">0</b></span>
        </div>
      </div>
    </div>

    <div class="grid" id="feat-grid"></div>

    <div class="monitor" id="monitor-grid"></div>

  </div>
</section>

<!-- ========== 文件页 ========== -->
<section id="view-files" class="hidden">
  <div class="file-bar">
    <div class="file-top">
      <button class="back" onclick="goUp()" title="上级">←</button>
      <div class="path" id="file-path">/</div>
      <button class="up" id="up-btn" onclick="document.getElementById('upfile').click()">⬆ 上传</button>
      <input type="file" id="upfile" hidden multiple onchange="upload(this)">
    </div>
    <div class="crumb" id="crumb"></div>
  </div>
  <div id="files-body">
    <div class="loading"><span class="spin"></span>加载中…</div>
  </div>
</section>

<!-- ========== 我的页 ========== -->
<section id="view-me" class="hidden">
  <div class="me">
    <div class="me-head">
      <span class="av">A</span>
      <div>
        <div class="t">admin</div>
        <div class="s" id="me-host">—</div>
      </div>
    </div>

    <div class="section" id="bk-section">
      <div class="sitem big" onclick="ddnas.startBackup()">
        <span class="ic">💾</span>
        <div class="lbl">立即备份<div class="desc" id="bk-quick-desc">递归上传选中目录到中间件</div></div>
        <span class="arr">›</span>
      </div>
      <div class="bk-panel">
        <div class="bk-row">
          <div class="bk-k">本地目录</div>
          <div class="bk-v" id="bk-dir">未选择</div>
          <button class="bk-btn" id="bk-pick" onclick="ddnas.chooseBackupDir()">选择</button>
        </div>
        <div class="bk-row">
          <div class="bk-k">远程路径</div>
          <input class="bk-input" id="bk-remote" type="text" placeholder="/手机备份/" />
          <button class="bk-btn" id="bk-save-remote" onclick="saveRemoteBase()">保存</button>
        </div>
        <div class="bk-row">
          <div class="bk-k">上次备份</div>
          <div class="bk-v" id="bk-last">—</div>
          <button class="bk-btn" id="bk-refresh-cfg" onclick="loadBackupConfig()">刷新</button>
        </div>
      </div>
    </div>

    <div class="section">
      <a href="/admin/" class="sitem" style="color:inherit;text-decoration:none">
        <span class="ic">⚙️</span>
        <div class="lbl">控制台<div class="desc">适配器配置、App 令牌、连接信息</div></div>
        <span class="arr">›</span>
      </a>
      <a href="/admin/adapter/node" class="sitem" style="color:inherit;text-decoration:none">
        <span class="ic">🖥</span>
        <div class="lbl">设备监控配置<div class="desc">node_exporter 地址等</div></div>
        <span class="arr">›</span>
      </a>
      <a href="/admin/adapter/openlist" class="sitem" style="color:inherit;text-decoration:none">
        <span class="ic">📁</span>
        <div class="lbl">文件服务配置<div class="desc">AList/OpenList 地址、令牌、根路径</div></div>
        <span class="arr">›</span>
      </a>
    </div>

    <div class="section">
      <div class="sitem">
        <span class="ic">ℹ️</span>
        <div class="lbl">版本<div class="desc">DDNAS v1.1 · 构建 <span id="me-build">20260903</span></div></div>
      </div>
      <div class="sitem">
        <span class="ic">🛰</span>
        <div class="lbl">主机<div class="desc" id="me-host2">—</div></div>
      </div>
    </div>
  </div>
</section>

<!-- ========== 底部 Tab ========== -->
<nav class="tabbar">
  <button id="tab-home" class="on" onclick="setTab('home')"><span class="ic">🏠</span><span class="lb">首页</span></button>
  <button id="tab-files" onclick="setTab('files')"><span class="ic">🗂</span><span class="lb">文件</span></button>
  <button id="tab-me" onclick="setTab('me')"><span class="ic">👤</span><span class="lb">我的</span></button>
</nav>

<div id="toast" class="toast"></div>

<script>
/* ========= 全局 401 拦截：session 失效自动跳登录页 ========= */
// 容器重装 / 密码变更后旧 session 失效，/portal/api/* 返回 401。
// 这里 monkey-patch fetch，捕获 401 后跳转 /admin/login，避免页面停在 401 JSON 上。
(function(){
  var _fetch=window.fetch;
  window.fetch=function(){
    return _fetch.apply(this,arguments).then(function(r){
      if(r.status===401){
        var u="/admin/login?redirect="+encodeURIComponent(location.pathname+location.search);
        location.href=u;
        // 返回永不 resolve 的 Promise，阻止后续 .then/.catch 执行
        return new Promise(function(){});
      }
      return r;
    });
  };
})();

/* ========= 工具 ========= */
function esc(s){return String(s==null?"":s).replace(/[&<>"]/g,c=>({"&":"&amp;","<":"&lt;",">":"&gt;",'"':"&quot;"}[c]));}
function escJS(s){return String(s==null?"":s).replace(/\\/g,"\\\\").replace(/'/g,"\\'");}
function fmtBytes(v){v=+v||0;const u=["B","KB","MB","GB","TB","PB"];let i=0;while(v>=1024&&i<u.length-1){v/=1024;i++;}return v.toFixed(v>=100?0:1)+" "+u[i];}
function pct(v){return Math.max(0,Math.min(100,+v||0)).toFixed(1)+"%";}
function toast(m){const t=document.getElementById("toast");t.textContent=m;t.classList.add("on");setTimeout(()=>t.classList.remove("on"),1800);}
function joinPath(base,name){base=base||"";name=name||"";if(!base)return name;return base.replace(/\/+$/,"")+"/"+name.replace(/^\/+/,"");}
function mediaExt(name){const ext=(String(name||"").split(".").pop()||"").toLowerCase();
  if(/^(mp4|mkv|mov|avi|webm|m4v|ts|m3u8|wmv|flv|rmvb)$/.test(ext))return"video";
  if(/^(mp3|flac|aac|m4a|wav|ogg|ape|wma)$/.test(ext))return"audio";
  if(/^(jpg|jpeg|png|gif|webp|bmp|heic|raw)$/.test(ext))return"image";
  if(/^(pdf|doc|docx|xls|xlsx|ppt|pptx|txt|md|csv|zip|rar|7z|tar|gz)$/.test(ext))return"doc";
  return "other";
}
// 浏览器环境 fallback：play 走 openVideoPlayer（已在 play() 内判断），
// 仅给 startBackup 一个提示。App 环境下 ddnas 已由 addJavascriptInterface 注入，不进这分支。
if(typeof ddnas==="undefined"){
  window.ddnas={
    startBackup(){toast("备份功能请在 App 内使用。");}
  };
}else{
  // App WebView 环境：系统导航栏/手势条遮挡更多，增大底部留白确保内容可拉到底
  document.body.style.paddingBottom="160px";
}

/* ========= 三栏切换 ========= */
let curTab="home";
function setTab(t){
  curTab=t;
  ["home","files","me"].forEach(k=>{
    document.getElementById("view-"+k).classList.toggle("hidden",k!==t);
    document.getElementById("tab-"+k).classList.toggle("on",k===t);
  });
  if(t==="home"&&!homeLoaded)loadHome();
  if(t==="files"&&!filesLoadedEver)loadFiles("");
  if(t==="me"){document.getElementById("me-host").textContent=window.location.host;document.getElementById("me-host2").textContent=window.location.host;loadBackupConfig();}
  // 滚动回到顶部
  window.scrollTo({top:0,behavior:"instant"});
}

/* ========= 功能宫格 ========= */
// 只保留已实现的核心功能，后续扩展再加回
const FEATURES=[
  {id:"cloud",label:"云盘",icon:"📁",cls:"c1",cap:"files",on(){setTab("files");}},
  {id:"backup",label:"手机备份",icon:"💾",cls:"c6",cap:"backup",on(){ddnas.startBackup();}},
];
let caps=new Set();
function renderGrid(){
  const g=document.getElementById("feat-grid");
  g.innerHTML=FEATURES.map(function(f){
    const ok=!f.cap||caps.has(f.cap);
    return '<div class="cell '+(ok?'':'disabled')+'" data-id="'+f.id+'"><div class="icon '+f.cls+'">'+f.icon+'</div><div class="label">'+f.label+'</div></div>';
  }).join("");
  g.querySelectorAll(".cell").forEach(function(el){
    el.addEventListener("click",function(){
      const f=FEATURES.find(function(x){return x.id===el.dataset.id;});if(!f)return;
      const ok2=!f.cap||caps.has(f.cap);
      if(!ok2){toast("功能未启用：请在控制台启用对应适配器");return;}
      f.on();
    });
  });
}

/* ========= 监控数据渲染 ========= */
let sys=null;        // 上次 /api/node/system 结果
let homeLoaded=false;
let pollTimer=null;

// 判断圆环配色
function barClass(p){p=+p||0;if(p>=90)return"err";if(p>=70)return"warn";return"ok";}
// 监控卡：横向长方块，标题+进度条+键值对，紧凑展示
function mCardHTML(opts){
  const p=Math.max(0,Math.min(100,+opts.percent||0));
  const vals=(opts.metrics||[]).map(function(m){return '<span>'+esc(m.k)+' <b>'+m.v+'</b></span>';}).join("");
  const right=opts.right?'<span class="m-right">'+opts.right+'</span>':'';
  const bar=opts.bar!==false?('<div class="m-bar '+barClass(p)+'"><i style="width:'+p.toFixed(0)+'%"></i></div>'):'';
  return '<div class="m-card">'+
    '<div class="m-hd"><div class="m-title '+(opts.err?'err':'')+'"><span class="dot"></span>'+esc(opts.title)+'</div>'+right+'</div>'+
    bar+
    (vals?'<div class="m-vals">'+vals+'</div>':'')+
  '</div>';
}

async function loadHome(){
  homeLoaded=false;  // 每次切到首页都重拉一次初始
  // 1) 发现适配器能力
  try{
    const r=await fetch("/portal/api/adapters_discovery");
    if(r.ok){const s=await r.json();(s.adapters||[]).forEach(a=>{if(a.enabled&&Array.isArray(a.capabilities))a.capabilities.forEach(function(c){caps.add(c);});});}
  }catch(e){}
  renderGrid();

  // 2) 系统信息立即拉一次 + 10s 轮询
  const doLoad=async (manual)=>{
    const box=document.getElementById("monitor-grid");
    setStat("loading",manual?"手动刷新中…":"连接中…");
    try{
      const t0=Date.now();
      const r=await fetch("/portal/api/node/system",{cache:"no-store"});
      if(!r.ok)throw new Error("HTTP "+r.status);
      sys=await r.json();
      homeLoaded=true;
      renderNasCard(sys);
      renderMonitor(sys);
      const ms=Date.now()-t0;
      setStat("ok","已连接 · "+ms+"ms"+(manual?"（手动）":""));
    }catch(e){
      homeLoaded=true;
      renderNasCard(null);
      // 保持 4 张监控卡骨架，展示错误摘要与「前往配置」入口，用户能一眼定位到问题
      box.innerHTML=
        monitorEmpty("CPU",e.message)+
        monitorEmpty("内存",e.message)+
        monitorEmpty("网络",e.message)+
        monitorEmpty("硬盘",e.message)+
        '<div style="grid-column:1/-1;margin-top:4px" class="tip-card">'+
          '无法获取设备信息（'+esc(e.message)+'）<br>'+
          '可能原因：<b>node 适配器未启用</b> / 内网地址填错 / 容器与 node_exporter 不通 / 服务未启动。<br>'+
          '<a href="/admin/adapter/node" style="color:var(--accent)">🧪 到控制台先点「测试连接」排查</a>'+
        '</div>';
      setStat("err","未连接："+e.message);
    }
  };
  await doLoad(false);
  if(pollTimer)clearInterval(pollTimer);
  pollTimer=setInterval(function(){doLoad(false);},10000);
}
let lastStat={mode:"",text:"",ts:0};
function setStat(mode,text){
  var el=document.getElementById("d-stat");if(!el)return;
  var rf=document.getElementById("d-refresh");if(rf)rf.style.display="inline-block";
  var mark=mode==="ok"?"🟢":mode==="err"?"🔴":"🟡";
  el.textContent=mark+" "+text;
  lastStat={mode:mode,text:text,ts:Date.now()};
}
function forceRefreshSystem(){
  if(!window.__doLoad){
    // 懒注入：重新进入首页就会在 doLoad 闭包外没法重入；这里直接重跑一次 fetch
    var el=document.getElementById("d-stat");if(el)el.textContent="🟡 刷新中…";
    fetch("/portal/api/node/system",{cache:"no-store"}).then(function(r){if(!r.ok)throw new Error("HTTP "+r.status);return r.json();}).then(function(s){
      sys=s;renderNasCard(s);renderMonitor(s);setStat("ok","已连接 · 手动刷新");
    }).catch(function(e){renderNasCard(null);setStat("err","刷新失败："+e.message);});
    return;
  }
  window.__doLoad(true);
}

/* ===== 备份配置面板：读 App 桥渲染，保存走 App 桥 ===== */
function fmtBackupTime(ts){
  ts=+ts||0;if(!ts)return "—";
  try{
    const d=new Date(ts);
    const p=n=>String(n).padStart(2,"0");
    return d.getFullYear()+"-"+p(d.getMonth()+1)+"-"+p(d.getDate())+" "+p(d.getHours())+":"+p(d.getMinutes());
  }catch(e){return "—";}
}
function loadBackupConfig(){
  const dirEl=document.getElementById("bk-dir");
  const remoteEl=document.getElementById("bk-remote");
  const lastEl=document.getElementById("bk-last");
  const quickDescEl=document.getElementById("bk-quick-desc");
  if(!dirEl)return;
  if(typeof ddnas==="undefined"||!ddnas.getBackupConfig){
    if(dirEl){dirEl.textContent="App 内可用";dirEl.classList.add("empty");}
    if(remoteEl)remoteEl.disabled=true;
    if(lastEl)lastEl.textContent="App 内可用";
    return;
  }
  let cfg=null;
  try{cfg=JSON.parse(ddnas.getBackupConfig());}catch(e){cfg=null;}
  if(!cfg){if(dirEl)dirEl.textContent="读取失败";return;}
  // 本地目录：hasDir=true 显示路径；hasDir=false 显示"未选择"
  if(cfg.hasDir){
    dirEl.textContent=cfg.dirDisplay||"已选择";
    dirEl.classList.remove("empty");
    if(quickDescEl)quickDescEl.textContent="递归上传 "+(cfg.dirDisplay||"所选目录")+" → "+(cfg.remoteBase||"/手机备份/");
  }else{
    dirEl.textContent="未选择（或权限已失效）";
    dirEl.classList.add("empty");
    if(quickDescEl)quickDescEl.textContent='点击下方"选择"按钮选本地目录';
  }
  // 远程路径：仅在为空时填默认占位
  if(remoteEl && !remoteEl.dataset.touched){
    remoteEl.value=cfg.remoteBase||"/手机备份/";
  }
  if(lastEl)lastEl.textContent=fmtBackupTime(cfg.lastBackupTime);
}
function saveRemoteBase(){
  const remoteEl=document.getElementById("bk-remote");
  if(!remoteEl)return;
  remoteEl.dataset.touched="1";
  let base=remoteEl.value.trim();
  if(!base){toast("远程路径不能为空");return;}
  // 规范：必须以 / 开头，以 / 结尾
  if(base.charAt(0)!=="/")base="/"+base;
  if(base.charAt(base.length-1)!=="/")base=base+"/";
  if(typeof ddnas==="undefined"||!ddnas.setRemoteBase){toast("请在 App 内使用");return;}
  try{
    ddnas.setRemoteBase(base);
    remoteEl.value=base;
    toast("已保存远程路径："+base,1500);
    loadBackupConfig();
  }catch(e){toast("保存失败："+e.message);}
}
function monitorEmpty(title,reason){
  // 占位监控卡：左侧灰色空环，右侧展示"—"；保持 2x2 网格稳定，不会从"4 卡"忽然变"1 条错误"页面跳动
  return '<div class="m-card">'+
    '<div class="title err"><span class="dot"></span>'+esc(title)+'</div>'+
    '<div class="row">'+
      '<div><div class="ring" style="--p:0"></div></div>'+
      '<div class="m-kv">'+
        '<span class="k">状态</span><span class="v">—</span>'+
        '<span class="k">数值</span><span class="v">—</span>'+
      '</div>'+
    '</div></div>';
}

/* NAS 卡片渲染：从 disks 汇总总容量，用 hostname/os 拼接展示 */
function renderNasCard(s){
  const modelEl=document.getElementById("d-model");
  const netEl=document.getElementById("d-net");
  const usedEl=document.getElementById("d-used");
  const totalEl=document.getElementById("d-total");
  const freeEl=document.getElementById("d-free");
  const barEl=document.getElementById("d-usage-bar");
  const descEl=document.getElementById("d-desc");
  if(!s){
    modelEl.textContent="DDNAS";netEl.textContent="未连接";
    usedEl.textContent="-";totalEl.textContent="-";freeEl.textContent="-";barEl.style.width="0%";
    descEl.textContent="启用并配置 node 适配器后显示设备信息";
    return;
  }
  // d-model 固定显示 DDNAS，不再用 hostname（node_exporter 主机名无意义）
  modelEl.textContent="DDNAS";
  const isWan=false;netEl.textContent=isWan?"外网":"内网";
  // 取容量最大的磁盘作为主盘展示（后端已过滤 tmpfs/overlay 等虚拟 FS）
  let mainDisk=null;
  (s.disks||[]).forEach(d=>{if(!mainDisk||+d.total_bytes>+mainDisk.total_bytes)mainDisk=d;});
  let totalBytes=mainDisk?+mainDisk.total_bytes:0;
  let usedBytes=mainDisk?+mainDisk.used_bytes:0;
  usedEl.textContent=fmtBytes(usedBytes);totalEl.textContent=fmtBytes(totalBytes);
  freeEl.textContent=fmtBytes(Math.max(0,totalBytes-usedBytes));
  const p=totalBytes>0?usedBytes/totalBytes*100:0;
  barEl.style.width=p.toFixed(1)+"%";
  const parts=[];if(s.os)parts.push(esc(s.os));if(s.arch)parts.push(esc(s.arch));if(s.kernel)parts.push(esc(String(s.kernel).slice(0,22)));
  const up=+s.uptime_seconds||0;const d=Math.floor(up/86400),h=Math.floor(up%86400/3600),m=Math.floor(up%3600/60);
  const upStr="运行 "+(d?d+"天":"")+(h?h+"时":"")+m+"分";
  // 开机时间：boot_time 是 epoch 秒，转可读 MM-DD HH:MM
  const bt=+s.boot_time||0;
  let bootStr="";
  if(bt){
    const bd=new Date(bt*1000);
    const pad=function(n){return String(n).padStart(2,"0");};
    bootStr="开机 "+pad(bd.getMonth()+1)+"-"+pad(bd.getDate())+" "+pad(bd.getHours())+":"+pad(bd.getMinutes());
  }
  parts.push(upStr+(bootStr?" · "+bootStr:""));
  descEl.textContent=parts.join(" · ")||"—";
}

function renderMonitor(s){
  const box=document.getElementById("monitor-grid");
  if(!s){box.innerHTML="";return;}
  const cpu=s.cpu||{};
  const mem=s.memory||{};
  const net=(s.network||[])[0]||{};

  const cpuPct=+cpu.usage_percent||0;
  const cpuHtml=mCardHTML({title:"CPU",percent:cpuPct,
    right:cpuPct.toFixed(1)+"%",
    metrics:[
      {k:"负载",v:(+cpu.load1||0).toFixed(2)},
      {k:"核心",v:cpu.cores?cpu.cores+"核":"—"}
    ]
  });
  const memPct=+mem.usage_percent||0;
  const memHtml=mCardHTML({title:"内存",percent:memPct,
    right:memPct.toFixed(1)+"%",
    metrics:[
      {k:"已用",v:fmtBytes(+mem.used_bytes||0)},
      {k:"总量",v:fmtBytes(+mem.total_bytes||0)}
    ]
  });
  // 网络：后端基于两次采样做差计算 B/s 速率，首次请求为 0
  const netHtml=mCardHTML({title:"网络",bar:false,
    right:"↓"+fmtBytes(+net.rx_rate||0)+"/s ↑"+fmtBytes(+net.tx_rate||0)+"/s",
    metrics:[
      {k:"累计接收",v:fmtBytes(+net.rx_bytes||0)},
      {k:"累计发送",v:fmtBytes(+net.tx_bytes||0)},
      {k:"网卡",v:esc(net.device||"—")}
    ]
  });
  // 温度：取最高值作为核心温度展示（node_hwmon_temp_celsius）
  let maxTemp=0,tempName="";
  (s.temps||[]).forEach(function(t){if(+t.value>maxTemp){maxTemp=+t.value;tempName=t.chip+"/"+t.name;}});
  const tempHtml=mCardHTML({title:"温度",bar:false,
    right:maxTemp>0?maxTemp.toFixed(1)+"°C":"无",
    metrics:maxTemp>0?[
      {k:"传感器",v:esc(tempName)},
      {k:"数量",v:(s.temps||[]).length+"个"}
    ]:[{k:"提示",v:"node_exporter 未启用 hwmon collector"}]
  });
  box.innerHTML=cpuHtml+memHtml+netHtml+tempHtml;
}

/* ========= 文件浏览 ========= */
let curFiles="";
let filesLoadedEver=false;
function loadFiles(p){
  curFiles=p||"";filesLoadedEver=true;
  const body=document.getElementById("files-body");
  const pathEl=document.getElementById("file-path");
  pathEl.textContent="/"+(curFiles||"");
  const upBtn=document.getElementById("up-btn");
  if(upBtn)upBtn.title="上传到当前目录: /"+(curFiles||"");
  renderCrumb(curFiles);
  body.innerHTML='<div class="loading"><span class="spin"></span>加载中…</div>';
  fetch("/portal/api/openlist/files/list?path="+encodeURIComponent(curFiles)).then(r=>{
    if(!r.ok)throw new Error("HTTP "+r.status);return r.json();
  }).then(resp=>{
    const items=(resp.items||[]).slice().sort((a,b)=>{
      const da=a.is_dir||a.type==="folder"||(a.is_dir==null&&String(a.name||"").lastIndexOf(".")<0)?1:0;
      const db=b.is_dir||b.type==="folder"||(b.is_dir==null&&String(b.name||"").lastIndexOf(".")<0)?1:0;
      if(da!==db)return db-da;return String(a.name||"").localeCompare(String(b.name||""));
    });
    if(!items.length){body.innerHTML='<div class="empty">空目录</div>';return;}
    body.innerHTML='<div class="flist">'+items.map(function(it){
      const name=esc(it.name||"");
      const isDir=!!(it.is_dir||it.type==="folder");
      const size=+it.size||0;
      const mt=it.modified||it.modified_at||it.created||"";
      const sub=(isDir?"":fmtBytes(size))+(mt?" / "+String(mt).slice(0,16):"");
      const rel=joinPath(curFiles,it.name||"");
      if(isDir){
        return '<div class="fitem" data-rel="'+esc(rel)+'" data-type="dir">'+
          '<div class="fic dir">📂</div><div class="fn"><div class="nm">'+name+'</div><div class="mt">'+esc(sub)+'</div></div>'+
          '<button class="fbtn" data-type="enter">进入</button></div>';
      }
      var kind=mediaExt(it.name||"");
      var icoClass=kind==="video"?"video":kind==="audio"?"audio":kind==="image"?"image":kind==="doc"?"doc":"";
      var icoChar=kind==="video"?"🎬":kind==="audio"?"🎵":kind==="image"?"🖼":kind==="doc"?"📄":"📦";
      var btn=(kind==="video"||kind==="audio")
        ?'<button class="fbtn" data-rel="'+esc(rel)+'" data-name="'+esc(it.name||"")+'" data-type="play">播放</button>'
        :(kind==="image"
          ?'<button class="fbtn" data-rel="'+esc(rel)+'" data-name="'+esc(it.name||"")+'" data-type="view">查看</button>'
          :'<button class="fbtn" data-rel="'+esc(rel)+'" data-name="'+esc(it.name||"")+'" data-type="download">下载</button>');
      return '<div class="fitem" data-type="file">'+
        '<div class="fic '+icoClass+'">'+icoChar+'</div><div class="fn"><div class="nm">'+name+'</div><div class="mt">'+esc(sub)+'</div></div>'+btn+'</div>';
    }).join("")+"</div>";

    body.querySelectorAll(".fitem").forEach(el=>{
      const t=el.dataset.type;
      const rel=el.dataset.rel||"";
      if(t==="dir"){
        el.addEventListener("click",e=>{
          if(e.target.dataset.type==="enter"||e.target.tagName!=="BUTTON")loadFiles(rel);
        });
      }
    });
    body.querySelectorAll('button[data-type="play"]').forEach(b=>{
      b.addEventListener("click",e=>{
        e.stopPropagation();play(b.dataset.rel||"");
      });
    });
    body.querySelectorAll('button[data-type="view"]').forEach(b=>{
      b.addEventListener("click",e=>{
        e.stopPropagation();viewImage(b.dataset.rel||"",b.dataset.name||"");
      });
    });
    body.querySelectorAll('button[data-type="download"]').forEach(b=>{
      b.addEventListener("click",e=>{
        e.stopPropagation();downloadFile(b.dataset.rel||"",b.dataset.name||"");
      });
    });
  }).catch(function(e){
    body.innerHTML='<div class="err">文件适配器未启用或获取失败：'+esc(e.message)+'<br><a href="/admin/adapter/openlist" style="color:var(--accent)">前往配置 -></a></div>';
  });
}
function goUp(){if(!curFiles)return;const i=curFiles.lastIndexOf("/");loadFiles(i<0?"":curFiles.slice(0,i));}
function renderCrumb(p){
  const c=document.getElementById("crumb");
  // onclick 属性值用双引号包裹，里面的 JS 字符串用单引号包裹，
  // 单引号在 JS 单引号字符串里需转义为 \'。Go raw string 里 \' 就是反斜杠+单引号两字符，
  // JS 解析为转义单引号。注意不能写成 \\' (两个反斜杠)，否则 JS 会把 \\ 当转义反斜杠，
  // ' 提前结束字符串导致整段脚本语法错误。
  let h="<a onclick=\"loadFiles('')\">根</a>";
  if(p){
    const segs=p.split("/");
    let acc="";
    segs.forEach(function(s){
      acc=acc?acc+"/"+s:s;
      h+="<span class=\"sep\">/</span><a onclick=\"loadFiles('"+escJS(acc)+"')\">"+esc(s)+"</a>";
    });
  }
  c.innerHTML=h;
}
function play(relPath){
  if(!relPath)return;
  // 逐段 encodeURIComponent，保留 "/" 分隔，Go {path...} 会正确捕获多段
  const url=location.origin+"/portal/api/openlist/files/stream/"+relPath.split("/").map(encodeURIComponent).join("/");
  const name=relPath.split("/").pop()||"播放";
  // App 原生桥可用时调 ExoPlayer（支持更多格式 + 硬解 + cookie 注入）
  if(typeof ddnas!=="undefined"&&typeof ddnas.playMedia==="function"){
    try{ddnas.playMedia(url);return;}catch(e){}
  }
  // 浏览器环境或桥不可用：HTML5 video 标签直接播放（浏览器支持的格式直接放）
  openVideoPlayer(url,name);
}
function openVideoPlayer(url,title){
  var ov=document.createElement("div");
  ov.style.cssText="position:fixed;inset:0;background:rgba(0,0,0,.95);z-index:9999;display:flex;align-items:center;justify-content:center;flex-direction:column";
  var v=document.createElement("video");
  v.src=url;v.controls=true;v.autoplay=true;
  v.style.cssText="max-width:100%;max-height:90vh;width:auto;height:auto";
  v.addEventListener("error",function(){
    v.style.display="none";
    // 主动 HEAD 探测一下回源情况，把状态码/Content-Type 显示出来，方便排查
    fetch(url,{method:"GET",credentials:"include"}).then(function(r){
      var ct=r.headers.get("Content-Type")||"—";
      var cl=r.headers.get("Content-Length")||"—";
      var cr=r.headers.get("Content-Range")||"—";
      var ac=r.headers.get("Accept-Ranges")||"—";
      var bodyText="";
      // 非 2xx 或非视频类 Content-Type 才尝试读 body 看错误信息
      if(r.status>=400||!/video|application\/octet-stream|mp4|mkv/i.test(ct)){
        return r.text().then(function(t){bodyText=(t||"").slice(0,300);}).catch(function(){}).then(function(){
          showDiag(r.status,ct,cl,cr,ac,bodyText);
        });
      }
      showDiag(r.status,ct,cl,cr,ac,bodyText);
    }).catch(function(e){
      showDiag("—","—","—","—","—","fetch 失败："+(e&&e.message||e));
    });
    function showDiag(status,ct,cl,cr,ac,body){
      var msg=document.createElement("div");
      msg.style.cssText="color:#f5a623;text-align:center;padding:20px;max-width:460px;font-size:13px;line-height:1.6";
      msg.innerHTML="<p style='font-size:15px;margin:0 0 8px'>无法播放此视频</p>"+
        "<p style='opacity:.7;word-break:break-all;margin:0 0 10px'>"+esc(url)+"</p>"+
        "<p style='opacity:.85;text-align:left;background:rgba(255,255,255,.06);padding:10px;border-radius:8px;font-family:monospace;font-size:12px'>"+
        "HTTP 状态：<b style='color:#fff'>"+esc(status)+"</b><br>"+
        "Content-Type：<b style='color:#fff'>"+esc(ct)+"</b><br>"+
        "Content-Length：<b style='color:#fff'>"+esc(cl)+"</b><br>"+
        "Content-Range：<b style='color:#fff'>"+esc(cr)+"</b><br>"+
        "Accept-Ranges：<b style='color:#fff'>"+esc(ac)+"</b>"+
        (body?"<br>响应体：<span style='color:#fff;word-break:break-all'>"+esc(body)+"</span>":"")+
        "</p>"+
        "<p style='opacity:.6;font-size:12px;margin:8px 0 0'>若状态 401/403：OpenList 令牌失效或缺少 sign；若 Content-Type 非 video/*：上游未识别为媒体；查看 docker logs 可见更详细日志。</p>";
      ov.appendChild(msg);
    }
  });
  var bar=document.createElement("div");
  bar.style.cssText="position:absolute;top:0;left:0;right:0;display:flex;justify-content:space-between;align-items:center;padding:12px 16px;background:linear-gradient(to bottom,rgba(0,0,0,.6),transparent)";
  var t=document.createElement("span");
  t.textContent=title;t.style.cssText="color:#fff;font-size:14px;max-width:70%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap";
  var c=document.createElement("button");
  c.textContent="\u2715";c.style.cssText="color:#fff;background:rgba(255,255,255,.2);border:none;width:32px;height:32px;border-radius:50%;font-size:16px;cursor:pointer";
  c.onclick=function(){ov.remove();};
  bar.appendChild(t);bar.appendChild(c);
  ov.appendChild(bar);ov.appendChild(v);
  document.body.appendChild(ov);
  v.play().catch(function(){});
}
function viewImage(relPath,name){
  if(!relPath)return;
  // 与 play 复用同一 stream 代理：浏览器 img 直接加载即可
  const url=location.origin+"/portal/api/openlist/files/stream/"+relPath.split("/").map(encodeURIComponent).join("/");
  const dispName=name||(relPath.split("/").pop()||"图片");
  // App 原生桥：用 PhotoView 等打开，体验优于 WebView 内联预览
  if(typeof ddnas!=="undefined"&&typeof ddnas.viewImage==="function"){
    try{ddnas.viewImage(url,dispName);return;}catch(e){}
  }
  // 浏览器环境或桥不可用：HTML5 <img> 内联预览
  openImageOverlay(url,dispName);
}
function openImageOverlay(url,title){
  var ov=document.createElement("div");
  ov.style.cssText="position:fixed;inset:0;background:rgba(0,0,0,.95);z-index:9999;display:flex;align-items:center;justify-content:center;flex-direction:column";
  var img=document.createElement("img");
  img.src=url;
  img.style.cssText="max-width:100%;max-height:90vh;width:auto;height:auto;object-fit:contain";
  img.addEventListener("error",function(){
    img.style.display="none";
    // 同视频：fetch 探测失败原因，显示状态码/Content-Type/响应体
    fetch(url,{method:"GET",credentials:"include"}).then(function(r){
      var ct=r.headers.get("Content-Type")||"—";
      var cl=r.headers.get("Content-Length")||"—";
      var bodyText="";
      if(r.status>=400||!/image|application\/octet-stream/.test(ct)){
        return r.text().then(function(t){bodyText=(t||"").slice(0,300);}).catch(function(){}).then(function(){
          showDiag(r.status,ct,cl,bodyText);
        });
      }
      showDiag(r.status,ct,cl,bodyText);
    }).catch(function(e){
      showDiag("—","—","—","fetch 失败："+(e&&e.message||e));
    });
    function showDiag(status,ct,cl,body){
      var msg=document.createElement("div");
      msg.style.cssText="color:#f5a623;text-align:center;padding:20px;max-width:460px;font-size:13px;line-height:1.6";
      msg.innerHTML="<p style='font-size:15px;margin:0 0 8px'>无法加载此图片</p>"+
        "<p style='opacity:.7;word-break:break-all;margin:0 0 10px'>"+esc(url)+"</p>"+
        "<p style='opacity:.85;text-align:left;background:rgba(255,255,255,.06);padding:10px;border-radius:8px;font-family:monospace;font-size:12px'>"+
        "HTTP 状态：<b style='color:#fff'>"+esc(status)+"</b><br>"+
        "Content-Type：<b style='color:#fff'>"+esc(ct)+"</b><br>"+
        "Content-Length：<b style='color:#fff'>"+esc(cl)+"</b>"+
        (body?"<br>响应体：<span style='color:#fff;word-break:break-all'>"+esc(body)+"</span>":"")+
        "</p>";
      ov.appendChild(msg);
    }
  });
  var bar=document.createElement("div");
  bar.style.cssText="position:absolute;top:0;left:0;right:0;display:flex;justify-content:space-between;align-items:center;padding:12px 16px;background:linear-gradient(to bottom,rgba(0,0,0,.6),transparent)";
  var t=document.createElement("span");
  t.textContent=title;t.style.cssText="color:#fff;font-size:14px;max-width:70%;overflow:hidden;text-overflow:ellipsis;white-space:nowrap";
  var c=document.createElement("button");
  c.textContent="\u2715";c.style.cssText="color:#fff;background:rgba(255,255,255,.2);border:none;width:32px;height:32px;border-radius:50%;font-size:16px;cursor:pointer";
  c.onclick=function(){ov.remove();};
  bar.appendChild(t);bar.appendChild(c);
  ov.appendChild(bar);ov.appendChild(img);
  document.body.appendChild(ov);
}
function downloadFile(relPath,name){
  if(!relPath){toast("无效路径");return;}
  const url=location.origin+"/portal/api/openlist/files/stream/"+relPath.split("/").map(encodeURIComponent).join("/");
  const dispName=name||(relPath.split("/").pop()||"文件");
  // App 原生桥：弹下载确认对话框，确认后写入 Download 目录
  if(typeof ddnas!=="undefined"&&typeof ddnas.downloadFile==="function"){
    try{ddnas.downloadFile(url,dispName);return;}catch(e){}
  }
  // 浏览器环境：直接打开下载链接（浏览器自带下载对话框）
  try{
    const a=document.createElement("a");
    a.href=url;a.download=dispName;a.target="_blank";a.rel="noopener";
    document.body.appendChild(a);a.click();a.remove();
  }catch(e){toast("下载失败："+(e&&e.message||e));}
}
async function upload(input){
  const files=input.files;if(!files||!files.length)return;
  const dir=curFiles||"";
  const dirDisp=dir?("/"+dir):"/";
  let ok=0,fail=0;const total=files.length;
  for(let i=0;i<files.length;i++){
    const f=files[i];
    const dest=joinPath(dir,f.name);
    toast("上传中 ("+(i+1)+"/"+total+") → "+dirDisp+"/"+f.name);
    try{
      const r=await fetch("/portal/api/openlist/files/upload?path="+encodeURIComponent(dest),{method:"POST",body:f,headers:{"Content-Type":"application/octet-stream"}});
      if(!r.ok)throw new Error("HTTP "+r.status);
      ok++;
    }catch(e){fail++;console.error("上传失败 "+f.name,e);}
  }
  input.value="";
  if(fail===0){toast("已上传 "+ok+" 个文件到 "+dirDisp,2000);}
  else if(ok===0){toast("上传失败 "+fail+" 个",3000);}
  else{toast("完成 "+ok+" 成功 / "+fail+" 失败",3000);}
  loadFiles(dir);
}

/* ========= 启动：按 URL ?tab= 或默认 home ========= */
(function(){
  const q=new URLSearchParams(location.search);const t=q.get("tab");
  setTab(t==="files"?"files":t==="me"?"me":"home");
})();
</script>
</body></html>`

// servePortal 返回 App 套壳加载的 /portal 页面；未登录跳转到 /admin/login。
// 页面直接返回内嵌的 portalSrc（跟随镜像更新，不再持久化到 /data，避免旧版不刷新）。
// 页面是纯静态 HTML，所有动态数据通过同源 /portal/api/* 获取，
// 内网地址（node_exporter:9100 / AList:5244 等）只写入 Go 配置，
// 在 adapter handler 内以容器 HTTP Client 调用，客户端永不触及内网。
func (s *Server) servePortal(w http.ResponseWriter, r *http.Request) {
	if !s.admin.LoggedIn(r) {
		http.Redirect(w, r, "/admin/login", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(portalSrc))
}
