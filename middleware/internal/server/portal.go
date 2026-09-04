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
.file-top{display:flex;align-items:center;gap:10px;min-height:46px}
.file-top .back{width:40px;height:40px;border-radius:12px;background:var(--surface2);border:1px solid var(--bd);display:inline-flex;align-items:center;justify-content:center;font-size:18px}
/* 路径字体放大到 15px：手机端单手阅读更舒适；加粗保持视觉层级 */
.file-top .path{flex:1;min-width:0;font-weight:700;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;font-size:15px}
.file-top .up{background:var(--accent);color:#fff;border-radius:12px;padding:9px 14px;font-size:14px;display:inline-flex;align-items:center;gap:6px;font-weight:600;min-height:36px}
.file-top .up:disabled{background:var(--muted2);opacity:.5;cursor:not-allowed}
/* 面包屑：字体 13px + 可点击块加 padding，手机端点击不费力；保留分隔符与高亮色 */
.crumb{display:flex;flex-wrap:wrap;align-items:center;gap:2px 4px;color:var(--muted);font-size:13px;line-height:1.5;overflow-x:auto;padding:2px 0}
.crumb a{color:var(--accent);white-space:nowrap;padding:4px 6px;border-radius:6px;-webkit-tap-highlight-color:transparent}
.crumb a:active{background:var(--chip)}
.crumb .sep{color:var(--muted2);padding:0 1px}
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
.bk-btn:disabled{opacity:.4;cursor:not-allowed}
.bk-row.warn .bk-v{color:var(--warn)}
/* 自动备份开关 toggle */
.bk-toggle{position:relative;display:inline-block;width:42px;height:24px;flex-shrink:0}
.bk-toggle input{opacity:0;width:0;height:0}
.bk-toggle-slider{
  position:absolute;cursor:pointer;inset:0;
  background:var(--chip);border:1px solid var(--bd);
  border-radius:12px;transition:.3s;
}
.bk-toggle-slider:before{
  content:"";position:absolute;height:18px;width:18px;left:2px;top:2px;
  background:var(--fg);border-radius:50%;transition:.3s;
}
.bk-toggle input:checked + .bk-toggle-slider{background:var(--accent);border-color:var(--accent)}
.bk-toggle input:checked + .bk-toggle-slider:before{transform:translateX(18px);background:#fff}

/* 备份进度条（内嵌面板，不弹窗） */
.bk-progress{margin-top:10px;padding:10px;background:var(--surface2);border-radius:10px;border:1px solid var(--bd)}
.bk-prog-head{display:flex;justify-content:space-between;align-items:center;font-size:12px;margin-bottom:6px}
.bk-prog-label{color:var(--fg);font-weight:600}
.bk-prog-right{display:flex;align-items:center;gap:8px}
.bk-prog-count{color:var(--accent);font-variant-numeric:tabular-nums}
.bk-prog-cancel{font-size:11px;color:var(--err);padding:2px 8px;border-radius:6px;border:1px solid var(--bd);background:var(--chip)}
.bk-prog-cancel:active{opacity:.6}
.bk-prog-bar{height:6px;background:var(--bd);border-radius:3px;overflow:hidden}
.bk-prog-fill{height:100%;background:var(--accent);transition:width .3s ease;border-radius:3px}
.bk-prog-cur{font-size:11px;color:var(--muted);margin-top:6px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.bk-prog-cur.err{color:var(--err)}
.bk-prog-cur.ok{color:var(--ok)}

/* 备份历史列表（最近 N 条，含失败文件展开） */
.bk-history{margin-top:6px;padding:10px;background:var(--surface2);border-radius:10px;border:1px solid var(--bd)}
.bk-hist-head{display:flex;justify-content:space-between;align-items:center;font-size:12px;margin-bottom:8px;color:var(--muted);gap:8px}
.bk-hist-actions{display:flex;gap:6px;flex-shrink:0}
.bk-hist-refresh{font-size:11px;color:var(--accent);padding:2px 8px;border-radius:6px;border:1px solid var(--bd);background:var(--chip)}
.bk-hist-refresh:active{opacity:.6}
.bk-hist-clear{font-size:11px;color:var(--err);padding:2px 8px;border-radius:6px;border:1px solid rgba(239,91,91,.35);background:rgba(239,91,91,.08)}
.bk-hist-clear:active{opacity:.6}
.bk-hist-empty{padding:14px;text-align:center;color:var(--muted2);font-size:12px}
.bk-hist-item{padding:8px 0;border-top:1px solid var(--bd);font-size:12px}
.bk-hist-item:first-child{border-top:0}
.bk-hist-row{display:flex;align-items:center;gap:8px;flex-wrap:wrap}
.bk-hist-time{color:var(--fg);font-variant-numeric:tabular-nums;flex:1;min-width:0;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.bk-hist-badge{font-size:10px;padding:1px 6px;border-radius:6px;background:var(--chip);color:var(--muted);white-space:nowrap}
.bk-hist-btns{display:flex;gap:6px;flex-shrink:0}
.bk-hist-btn{font-size:10px;padding:1px 7px;border-radius:6px;border:1px solid var(--bd);background:var(--chip);color:var(--muted);white-space:nowrap}
.bk-hist-btn:active{opacity:.6}
.bk-hist-btn.del{color:var(--err);border-color:rgba(239,91,91,.3)}
.bk-hist-btn.retry{color:var(--accent);border-color:rgba(56,130,255,.3)}
.bk-hist-badge.ok{background:rgba(37,194,117,.12);color:var(--ok)}
.bk-hist-badge.warn{background:rgba(245,166,35,.14);color:var(--warn)}
.bk-hist-badge.err{background:rgba(239,91,91,.12);color:var(--err)}
.bk-hist-stats{margin-top:4px;color:var(--muted);font-size:11px;display:flex;gap:10px;flex-wrap:wrap}
.bk-hist-stats b{color:var(--fg);font-weight:600}
.bk-hist-failed{margin-top:6px;padding:6px 8px;background:var(--card);border-radius:6px;border:1px dashed var(--bd);font-size:11px;color:var(--muted)}
.bk-hist-failed .fl-head{color:var(--err);font-weight:600;margin-bottom:3px}
.bk-hist-failed .fl-list{white-space:pre-wrap;word-break:break-all;line-height:1.5;max-height:120px;overflow:auto}

/* 远程目录浏览弹层 */
.bk-browse{position:fixed;inset:0;background:rgba(0,0,0,.45);z-index:50;display:flex;flex-direction:column;justify-content:center;align-items:center;overscroll-behavior:contain;-webkit-transform:translateZ(0);transform:translateZ(0)}
.bk-browse-card{background:var(--card,#fff);margin:16px;border:1px solid var(--bd,rgba(0,0,0,.12));border-radius:14px;height:80vh;max-height:80vh;width:calc(100% - 32px);max-width:520px;display:flex;flex-direction:column;overflow:hidden;box-shadow:0 8px 32px rgba(0,0,0,.25)}
.bk-browse-head{display:flex;align-items:center;gap:8px;padding:12px 14px;border-bottom:1px solid var(--bd,rgba(0,0,0,.08));flex-shrink:0}
.bk-browse-title{font-size:14px;font-weight:600;flex:1}
.bk-browse-path{font-size:11px;color:var(--muted);padding:6px 14px;border-bottom:1px solid var(--bd,rgba(0,0,0,.08));word-break:break-all;flex-shrink:0}
.bk-browse-list{flex:1;min-height:200px;overflow:auto;padding:4px 0;-webkit-overflow-scrolling:touch}
.bk-browse-item{display:flex;align-items:center;gap:10px;padding:10px 14px;font-size:13px}
.bk-browse-item:active{background:var(--surface2)}
.bk-browse-item .ic{font-size:16px;opacity:.8}
.bk-browse-item .nm{flex:1;min-width:0;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.bk-browse-empty{padding:24px;text-align:center;color:var(--muted);font-size:13px}
.bk-browse-foot{display:flex;gap:8px;padding:10px 14px;border-top:1px solid var(--bd,rgba(0,0,0,.08));flex-shrink:0}
.bk-browse-foot .bk-btn{flex:1;padding:9px 0;text-align:center;font-size:13px;font-weight:600}

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

/* ===== 下载页（DDM3U8 任务管理） ===== */
.dl-bar{position:sticky;top:0;z-index:10;background:var(--bg);display:flex;align-items:center;gap:10px;padding:10px 14px;border-bottom:1px solid transparent}
.dl-bar .back{width:40px;height:40px;border-radius:12px;background:var(--surface2);border:1px solid var(--bd);display:inline-flex;align-items:center;justify-content:center;font-size:18px}
.dl-title{flex:1;font-weight:700;font-size:16px}
.dl-refresh{width:34px;height:34px;border-radius:10px;background:var(--surface2);border:1px solid var(--bd);font-size:15px;display:inline-flex;align-items:center;justify-content:center}
.dl-refresh:active{opacity:.6}
.dl-page{padding:4px 14px 12px}
.dl-submit{background:var(--card);border:1px solid var(--bd);border-radius:14px;padding:12px;margin-bottom:12px;display:flex;flex-direction:column;gap:8px}
.dl-submit textarea,.dl-submit input{width:100%;font:inherit;font-size:13px;color:var(--fg);background:var(--surface2);border:1px solid var(--bd);border-radius:8px;padding:8px 10px;box-sizing:border-box}
.dl-submit textarea{min-height:64px;resize:vertical;line-height:1.4}
.dl-submit textarea:focus,.dl-submit input:focus{outline:none;border-color:var(--accent)}
.dl-submit .dl-go{background:var(--accent);color:#fff;border-radius:10px;padding:10px;font-size:14px;font-weight:600}
.dl-submit .dl-go:active{opacity:.7}
.dl-submit .dl-go:disabled{background:var(--muted2);opacity:.5;cursor:not-allowed}
.dl-stat{font-size:11px;color:var(--muted);text-align:right}
.dl-list{display:flex;flex-direction:column;gap:8px}
.dl-task{background:var(--card);border:1px solid var(--bd);border-radius:12px;padding:10px 12px}
.dl-task-head{display:flex;align-items:center;gap:8px;flex-wrap:wrap}
.dl-task-name{flex:1;min-width:0;font-size:13px;font-weight:600;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.dl-badge{font-size:10px;padding:1px 6px;border-radius:6px;background:var(--chip);color:var(--muted);white-space:nowrap}
.dl-badge.queued{background:rgba(52,120,246,.12);color:var(--accent)}
.dl-badge.running{background:rgba(37,194,117,.12);color:var(--ok)}
.dl-badge.paused{background:rgba(245,166,35,.14);color:var(--warn)}
.dl-badge.done{background:rgba(37,194,117,.12);color:var(--ok)}
.dl-badge.error{background:rgba(239,91,91,.12);color:var(--err)}
.dl-task-meta{font-size:11px;color:var(--muted);margin-top:4px;display:flex;gap:10px;flex-wrap:wrap}
.dl-task-meta b{color:var(--fg);font-weight:600}
.dl-task-log{font-size:11px;color:var(--muted);margin-top:4px;white-space:pre-wrap;word-break:break-all;max-height:60px;overflow:auto;background:var(--surface2);border-radius:6px;padding:4px 6px;line-height:1.4}
.dl-actions{display:flex;gap:6px;margin-top:8px;flex-wrap:wrap}
.dl-act{font-size:11px;padding:4px 10px;border-radius:6px;border:1px solid var(--bd);background:var(--chip);color:var(--fg)}
.dl-act:active{opacity:.6}
.dl-act.danger{color:var(--err);border-color:var(--err)}
.dl-empty{padding:24px;text-align:center;color:var(--muted);font-size:13px}

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
        <div class="txt" style="display:flex;align-items:center;gap:8px;width:100%">
          <span class="mod" id="d-model">—</span>
          <span style="flex:1;min-width:8px"></span>
          <span style="font-size:11px;opacity:.85;white-space:nowrap;text-align:right;overflow:hidden;text-overflow:ellipsis;flex-shrink:0" id="d-stat">初始化中…</span>
          <button id="d-refresh" style="font-size:13px;color:#fff;opacity:.85;background:rgba(255,255,255,.18);border-radius:999px;width:26px;height:26px;display:none;align-items:center;justify-content:center;flex-shrink:0" onclick="forceRefreshSystem()">↻</button>
        </div>
        <div style="font-size:12px;opacity:.85;word-break:break-word;line-height:1.4" id="d-desc">家庭私有云 · 中间件 v1</div>
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
      <div class="sitem big" id="bk-quick" onclick="onQuickBackup()">
        <span class="ic">💾</span>
        <div class="lbl"><span id="bk-quick-label">立即备份</span><div class="desc" id="bk-quick-desc">递归上传选中目录到中间件</div></div>
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
          <input class="bk-input" id="bk-remote" type="text" placeholder="/手机备份/" oninput="toggleBkSave()" />
          <button class="bk-btn" id="bk-save-remote" onclick="saveRemoteBase()" disabled title="根目录不可保存，请进入子目录">保存</button>
          <button class="bk-btn" onclick="browseRemoteDir()">浏览</button>
        </div>
        <div class="bk-row">
          <div class="bk-k">上次备份</div>
          <div class="bk-v" id="bk-last">—</div>
          <button class="bk-btn" id="bk-refresh-cfg" onclick="loadBackupConfig()">刷新</button>
        </div>
        <div class="bk-row">
          <div class="bk-k">自动备份</div>
          <div class="bk-v" id="bk-auto-desc" style="font-size:12px;color:var(--muted)">充电+Wi-Fi 时每 15 分钟自动增量备份</div>
          <label class="bk-toggle"><input type="checkbox" id="bk-auto" onchange="onToggleAutoBackup(this.checked)" /><span class="bk-toggle-slider"></span></label>
        </div>
        <div class="bk-progress" id="bk-progress" style="display:none">
          <div class="bk-prog-head">
            <span class="bk-prog-label" id="bk-prog-label">备份中</span>
            <div class="bk-prog-right">
              <span class="bk-prog-count" id="bk-prog-count">0/0</span>
              <button class="bk-prog-cancel" id="bk-prog-cancel" onclick="ddnas.cancelBackup()">取消</button>
            </div>
          </div>
          <div class="bk-prog-bar"><div class="bk-prog-fill" id="bk-prog-fill" style="width:0%"></div></div>
          <div class="bk-prog-cur" id="bk-prog-cur">—</div>
        </div>
        <!-- 备份历史：从 SQLite 拉取最近 10 条，含失败文件列表（可展开收起） -->
        <div class="bk-history" id="bk-history">
          <div class="bk-hist-head">
            <span>备份历史</span>
            <div class="bk-hist-actions">
              <button class="bk-hist-refresh" id="bk-hist-refresh" onclick="loadBackupHistory()">刷新</button>
              <button class="bk-hist-clear" id="bk-hist-clear" onclick="clearBackupHistory()">清空</button>
            </div>
          </div>
          <div class="bk-hist-empty" id="bk-hist-empty">加载中…</div>
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

<!-- ========== 下载页（DDM3U8 任务管理，从首页宫格进入） ========== -->
<section id="view-download" class="hidden">
  <div class="dl-bar">
    <button class="back" onclick="setTab('home')" title="返回首页">←</button>
    <div class="dl-title">下载任务</div>
    <button class="dl-refresh" onclick="loadDownloadTasks()" title="刷新">↻</button>
  </div>
  <div class="dl-page">
    <!-- 提交表单：url 必填，DDM3U8 会从文本里正则识别多个 m3u8 链接 -->
    <div class="dl-submit">
      <textarea id="dl-url" placeholder="粘贴 m3u8 链接（支持多个，自动识别）"></textarea>
      <input id="dl-name" type="text" placeholder="文件名前缀（可选，默认 video）" />
      <input id="dl-referer" type="text" placeholder="Referer（可选，防盗链站点需要）" />
      <input id="dl-subpath" type="text" placeholder="子目录（可选，仅一层名）" />
      <button class="dl-go" id="dl-go" onclick="submitDownload()">提交下载</button>
      <div class="dl-stat" id="dl-stat">活跃 <b id="dl-active">0</b>/<b id="dl-max">0</b> 并发</div>
    </div>
    <!-- 任务列表：从 /portal/api/download/tasks 实时拉取，DDM3U8 状态中文 → 徽章映射 -->
    <div class="dl-list" id="dl-list"><div class="dl-empty">加载中…</div></div>
  </div>
</section>

<!-- ========== 底部 Tab ========== -->
<nav class="tabbar">
  <button id="tab-home" class="on" onclick="setTab('home')"><span class="ic">🏠</span><span class="lb">首页</span></button>
  <button id="tab-files" onclick="setTab('files')"><span class="ic">🗂</span><span class="lb">文件</span></button>
  <button id="tab-me" onclick="setTab('me')"><span class="ic">👤</span><span class="lb">我的</span></button>
</nav>

<div id="toast" class="toast"></div>

<!-- 远程目录浏览模态窗（备份模块内嵌，非 AlertDialog） -->
<div class="bk-browse" id="bk-browse" style="display:none" onclick="closeBrowse()">
  <div class="bk-browse-card" onclick="event.stopPropagation()">
    <div class="bk-browse-head">
      <button class="bk-btn" onclick="browseUp()">上级</button>
      <div class="bk-browse-title">选择远程目录</div>
      <button class="bk-btn" onclick="closeBrowse()">取消</button>
    </div>
    <div class="bk-browse-path" id="bk-browse-path">/</div>
    <div class="bk-browse-list" id="bk-browse-list"></div>
    <div class="bk-browse-foot">
      <button class="bk-btn" onclick="browsePick()">选择此目录</button>
    </div>
  </div>
</div>

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
/* 取用户访问中间件用的 IP/主机名（即 NAS 在局域网的入口地址）。
   用 hostname 而非 host，省去端口后缀，更紧凑。 */
function lanIP(){
  var h=location.hostname||"";
  return h||"—";
}
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
    startBackup(){toast("备份功能请在 App 内使用。");return "started";},
    cancelBackup(){toast("备份功能请在 App 内使用。");}
  };
}else{
  // App WebView 环境：系统导航栏/手势条遮挡更多，增大底部留白确保内容可拉到底
  document.body.style.paddingBottom="160px";
}

/* ========= 三栏切换 ========= */
let curTab="home";
function setTab(t){
  curTab=t;
  // view 容器：含 download（非 tabbar 页，从首页宫格进入）
  ["home","files","me","download"].forEach(k=>{
    const el=document.getElementById("view-"+k);
    if(el)el.classList.toggle("hidden",k!==t);
  });
  // tabbar 高亮：只有 home/files/me 三栏，download 不高亮任何 tab
  ["home","files","me"].forEach(k=>{
    const el=document.getElementById("tab-"+k);
    if(el)el.classList.toggle("on",k===t);
  });
  if(t==="home"&&!homeLoaded)loadHome();
  if(t==="files"&&!filesLoadedEver)loadFiles("");
  if(t==="me"){document.getElementById("me-host").textContent=window.location.host;document.getElementById("me-host2").textContent=window.location.host;loadBackupConfig();loadBackupHistory();}
  if(t==="download")loadDownloadTasks();
  // 滚动回到顶部
  window.scrollTo({top:0,behavior:"instant"});
}

/* ========= 功能宫格 ========= */
// 只保留已实现的核心功能，后续扩展再加回
const FEATURES=[
  {id:"cloud",label:"云盘",icon:"📁",cls:"c1",cap:"files",on(){setTab("files");}},
  {id:"backup",label:"手机备份",icon:"💾",cls:"c6",cap:"backup",on(){onQuickBackup();}},
  {id:"download",label:"下载",icon:"⬇️",cls:"c2",cap:"download",on(){setTab("download");}},
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
      setStat("ok","",ms);
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
      setStat("err","");
    }
  };
  await doLoad(false);
  if(pollTimer)clearInterval(pollTimer);
  pollTimer=setInterval(function(){doLoad(false);},10000);
}
let lastStat={mode:"",text:"",ts:0,ms:0};
/* 连接状态展示：ok=绿圈+延迟 / loading=黄圈+连接中 / err=红圈+检查网络。
   详细错误信息已在监控卡区域展示，d-stat 只保留简短状态，避免文字过长挤压布局。 */
function setStat(mode,text,ms){
  var el=document.getElementById("d-stat");if(!el)return;
  var rf=document.getElementById("d-refresh");if(rf)rf.style.display="inline-block";
  var mark=mode==="ok"?"🟢":mode==="err"?"🔴":"🟡";
  var disp;
  if(mode==="ok"){disp=(ms!=null&&ms!==undefined)?(ms+"ms"):"已连接";}
  else if(mode==="loading"){disp=text||"连接中…";}
  else if(mode==="err"){disp="检查网络";}
  else{disp=text||"";}
  el.textContent=mark+" "+disp;
  lastStat={mode:mode,text:disp,ts:Date.now(),ms:ms||0};
}
function forceRefreshSystem(){
  if(!window.__doLoad){
    // 懒注入：重新进入首页就会在 doLoad 闭包外没法重入；这里直接重跑一次 fetch
    setStat("loading","刷新中…");
    var t0=Date.now();
    fetch("/portal/api/node/system",{cache:"no-store"}).then(function(r){if(!r.ok)throw new Error("HTTP "+r.status);return r.json();}).then(function(s){
      sys=s;renderNasCard(s);renderMonitor(s);setStat("ok","",Date.now()-t0);
    }).catch(function(e){renderNasCard(null);setStat("err","");});
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
  toggleBkSave(); // 回填后刷新保存按钮可用性（根目录禁用）
  if(lastEl)lastEl.textContent=fmtBackupTime(cfg.lastBackupTime);
  // 自动备份开关
  const autoEl=document.getElementById("bk-auto");
  if(autoEl){autoEl.checked=!!cfg.autoBackup;}
}
/* ========= 备份历史（SQLite 持久化，由中间件 /portal/api/backup/history 提供） =========
 * 备份完成后 App 调 POST 上报，前端拉取最近 10 条渲染。
 * 失败文件列表默认收起，点击条目展开。设计目标：
 *   - 用户能直观看到"最近几次备份成功吗？失败的是哪些文件？"
 *   - 失败文件名按 "," 分隔换行展示，方便用户定位哪些照片/视频没传上去
 */
function loadBackupHistory(){
  const listHost=document.getElementById("bk-history");
  const emptyEl=document.getElementById("bk-hist-empty");
  if(!listHost)return;  // 不在 me 页面
  // 移除旧条目（保留 head + empty 占位）
  listHost.querySelectorAll(".bk-hist-item").forEach(el=>el.remove());
  if(emptyEl)emptyEl.textContent="加载中…";
  fetch("/portal/api/backup/history?limit=10").then(r=>{
    if(!r.ok)throw new Error("HTTP "+r.status);
    return r.json();
  }).then(resp=>{
    const records=resp.records||[];
    if(!records.length){
      if(emptyEl)emptyEl.textContent="暂无备份记录";
      return;
    }
    if(emptyEl)emptyEl.style.display="none";
    records.forEach(rec=>{
      const item=document.createElement("div");
      item.className="bk-hist-item";
      // 状态徽章：failed>0 标 warn，全失败标 err，否则 ok
      const total=+rec.total||0,failed=+rec.failed||0,success=+rec.success||0;
      let badgeCls="ok",badgeTxt="成功";
      if(failed>0&&failed<total){badgeCls="warn";badgeTxt="部分失败";}
      else if(failed>0&&total>0&&failed>=total){badgeCls="err";badgeTxt="失败";}
      else if(total===0){badgeCls="";badgeTxt="空"; }
      const failedList=Array.isArray(rec.failed_list)?rec.failed_list:[];
      const failedBlock=failedList.length
        ? '<div class="bk-hist-failed"><div class="fl-head">失败 '+failedList.length+' 个：</div><div class="fl-list">'+failedList.map(esc).join("<br>")+'</div></div>'
        : '';
      // 行内操作按钮：失败记录显示"重试"（调 App 桥 ddnas.startBackup 重新发起备份）；
      // 每条都可"删除"（调 DELETE /portal/api/backup/history/{id}）
      const retryBtn=(failed>0)
        ? '<button class="bk-hist-btn retry" onclick="retryBackup(event)">重试</button>'
        : '';
      const delBtn='<button class="bk-hist-btn del" onclick="deleteBackupRecord(event,'+(+rec.id||0)+')">删除</button>';
      item.innerHTML=
          '<div class="bk-hist-row">'+
            '<span class="bk-hist-time">'+esc(fmtBackupTime(rec.ts))+'</span>'+
            '<span class="bk-hist-badge '+badgeCls+'">'+badgeTxt+'</span>'+
            '<span class="bk-hist-btns">'+retryBtn+delBtn+'</span>'+
          '</div>'+
          '<div class="bk-hist-stats">'+
            '<span>总数 <b>'+total+'</b></span>'+
            '<span>成功 <b>'+success+'</b></span>'+
            '<span>失败 <b>'+failed+'</b></span>'+
            '<span>耗时 <b>'+fmtDurationMs(+rec.duration_ms||0)+'</b></span>'+
          '</div>'+
          failedBlock;
      listHost.appendChild(item);
    });
  }).catch(e=>{
    if(emptyEl)emptyEl.textContent="加载失败："+e.message;
  });
}
/* ========= 备份历史操作：清空 / 删除单条 / 重试 =========
 * - 清空：DELETE /portal/api/backup/history，避免记录长期积累
 * - 删除：DELETE /portal/api/backup/history/{id}，删单条
 * - 重试：调 App JS 桥 ddnas.startBackup() 重新发起增量备份
 *   （仅在 App WebView 内可用；浏览器访问 portal 时提示用户去 App 端）
 */
function clearBackupHistory(){
  if(!confirm("确认清空全部备份历史？此操作不可撤销。"))return;
  fetch("/portal/api/backup/history",{method:"DELETE",credentials:"same-origin"})
    .then(r=>r.json())
    .then(resp=>{
      if(resp.error){toast("清空失败："+resp.error);return;}
      toast("已清空备份历史");
      loadBackupHistory();
    })
    .catch(e=>toast("清空失败："+e.message));
}
function deleteBackupRecord(ev,id){
  if(ev&&ev.stopPropagation)ev.stopPropagation();
  if(!id||id<=0){toast("无效记录");return;}
  if(!confirm("删除这条备份记录？"))return;
  fetch("/portal/api/backup/history/"+encodeURIComponent(id),{method:"DELETE",credentials:"same-origin"})
    .then(r=>r.json())
    .then(resp=>{
      if(resp.error){toast("删除失败："+resp.error);return;}
      toast("已删除");
      loadBackupHistory();
    })
    .catch(e=>toast("删除失败："+e.message));
}
function retryBackup(ev){
  if(ev&&ev.stopPropagation)ev.stopPropagation();
  // App WebView 注入了 ddnas 桥时直接发起备份；浏览器访问则提示
  if(typeof ddnas!=="undefined"&&ddnas&&typeof ddnas.startBackup==="function"){
    const state=ddnas.startBackup();
    if(state==="running"){toast("已有备份在运行中");}
    else if(state==="started"){toast("已发起备份，请观察进度条");}
    else if(state==="noDir"){toast("请先选择备份目录");}
    else{toast("已发起备份");}
  }else{
    toast("请在 App 内点击重试，或重新发起备份");
  }
}
// 毫秒 → "1m23s" / "42s" 紧凑展示
function fmtDurationMs(ms){
  ms=+ms||0;
  if(ms<1000)return ms+"ms";
  const s=Math.round(ms/1000);
  if(s<60)return s+"s";
  const m=Math.floor(s/60),rs=s%60;
  return m+"m"+(rs>0?rs+"s":"");
}
// 根据远程路径输入框值切换"保存"按钮可用性：根目录(/或空)禁用，与上传按钮策略一致
function toggleBkSave(){
  const remoteEl=document.getElementById("bk-remote");
  const saveBtn=document.getElementById("bk-save-remote");
  if(!remoteEl||!saveBtn)return;
  const v=remoteEl.value.trim().replace(/^\/+/,"").replace(/\/+$/,"");
  saveBtn.disabled = v === "";
  saveBtn.title = v === "" ? "根目录不可保存，请进入子目录" : "";
}
function saveRemoteBase(){
  const remoteEl=document.getElementById("bk-remote");
  if(!remoteEl)return;
  remoteEl.dataset.touched="1";
  let base=remoteEl.value.trim();
  if(!base){toast("远程路径不能为空");return;}
  // 根目录不可保存：openlist 根挂载通常只读，与上传按钮策略一致
  if(base.replace(/^\/+/,"").replace(/\/+$/,"")===""){
    toast("根目录不可保存，请进入子目录");
    return;
  }
  if(base.charAt(0)!=="/")base="/"+base;
  if(base.charAt(base.length-1)!=="/")base=base+"/";
  remoteEl.value=base;
  remoteEl.disabled=true;
  var saveBtn=document.getElementById("bk-save-remote");
  if(saveBtn)saveBtn.textContent="验证中…";
  // 验证远程路径：先 mkdir 确保目录存在，再 upload 探针文件验证可写性。
  // 仅 mkdir 不够——openlist 挂载只读存储时 mkdir 可能假成功，但真正写文件
  // 才会暴露权限问题，避免"路径验证成功却备份不了"
  fetch("/portal/api/files/mkdir?path="+encodeURIComponent(base),{method:"POST",credentials:"same-origin"})
    .then(r=>r.json())
    .then(resp=>{
      if(!resp.ok){
        if(saveBtn){saveBtn.textContent="保存";remoteEl.disabled=false;}
        toast("路径不可用："+(resp.error||resp.message||"创建目录失败，请检查 OpenList 挂载"),3000);
        return;
      }
      // mkdir 成功，再写探针验证可写性（探针小文件，下次覆盖，不残留多份）
      if(saveBtn)saveBtn.textContent="验证可写…";
      fetch("/portal/api/files/upload?path="+encodeURIComponent(base+".ddnas_probe"),{method:"POST",body:new Blob(["probe"]),credentials:"same-origin"})
        .then(r2=>r2.json().catch(()=>({ok:r2.ok})))
        .then(p2=>{
          if(saveBtn){saveBtn.textContent="保存";remoteEl.disabled=false;}
          if(p2.ok){
            if(typeof ddnas!=="undefined"&&ddnas.setRemoteBase){
              try{ddnas.setRemoteBase(base);}catch(e){}
            }
            toast("路径验证成功（可写）："+base,1500);
            loadBackupConfig();
          }else{
            toast("目录已创建但不可写入："+(p2.error||p2.message||"请检查 OpenList 挂载是否只读"),3000);
          }
        })
        .catch(e=>{
          if(saveBtn){saveBtn.textContent="保存";remoteEl.disabled=false;}
          toast("可写性验证失败："+e.message,3000);
        });
    })
    .catch(e=>{
      if(saveBtn){saveBtn.textContent="保存";remoteEl.disabled=false;}
      toast("验证失败："+e.message+"（请检查 OpenList 配置）",3000);
    });
}

/* ========= 备份按钮状态切换 + 进度接收（原生推送 __onBackupProgress） ========= */
// bkPhase 镜像原生 BackupService.Progress 状态：idle/scanning/running/done/error。
// 主按钮"立即备份"在运行期间切换为"取消备份"，避免重复点击触发并发备份；
// 进度区内嵌取消按钮，运行期间可见。
var bkPhase="idle";
function onQuickBackup(){
  if(typeof ddnas==="undefined"||!ddnas.startBackup)return;
  // 进行中：转为取消
  if(bkPhase==="running"||bkPhase==="scanning"){
    if(ddnas.cancelBackup){ddnas.cancelBackup();toast("正在取消当前备份…");}
    return;
  }
  // 原生侧 isRunning 拦截 race，返回 "running" 时给提示
  const r=ddnas.startBackup();
  if(r==="running")toast("备份进行中，请先取消或等待完成");
}
// 自动备份开关：调原生桥 setAutoBackup(true/false)，注册/取消 WorkManager 定时任务
function onToggleAutoBackup(on){
  if(typeof ddnas==="undefined"||!ddnas.setAutoBackup){
    toast("自动备份请在 App 内使用");
    // 回滚开关
    const autoEl=document.getElementById("bk-auto");
    if(autoEl)autoEl.checked=!on;
    return;
  }
  try{
    ddnas.setAutoBackup(on);
    toast(on?"已开启自动备份（充电+Wi-Fi 时每 15 分钟）":"已关闭自动备份",1200);
  }catch(e){
    toast("设置失败："+e.message);
    const autoEl=document.getElementById("bk-auto");
    if(autoEl)autoEl.checked=!on;
  }
}
// 由 MainActivity evaluateJavascript 推送：__onBackupProgress(<Progress JSON 对象>)
window.__onBackupProgress=function(p){
  if(p==null)return;
  if(typeof p==="string"){try{p=JSON.parse(p);}catch(e){return;}}
  bkPhase=p.phase||"idle";
  const box=document.getElementById("bk-progress");
  const labelEl=document.getElementById("bk-prog-label");
  const countEl=document.getElementById("bk-prog-count");
  const fillEl=document.getElementById("bk-prog-fill");
  const curEl=document.getElementById("bk-prog-cur");
  const quickLabel=document.getElementById("bk-quick-label");
  const cancelBtn=document.getElementById("bk-prog-cancel");
  const running=bkPhase==="running"||bkPhase==="scanning";
  // 主按钮文案切换（进行中→"取消备份"）
  if(quickLabel)quickLabel.textContent=running?"取消备份":"立即备份";
  // 取消按钮显隐
  if(cancelBtn)cancelBtn.style.display=running?"inline-block":"none";
  // 进度区显隐：idle 隐藏，其余显示
  if(box)box.style.display=(bkPhase==="idle")?"none":"block";
  if(labelEl){
    labelEl.textContent=bkPhase==="scanning"?"扫描中…"
      :bkPhase==="running"?"备份中"
      :bkPhase==="done"?"完成"
      :bkPhase==="error"?"出错":"";
  }
  if(bkPhase==="running"){
    const total=+p.total||0,done=+p.done||0;
    if(countEl)countEl.textContent=done+"/"+total;
    if(fillEl)fillEl.style.width=(total>0?Math.round(done/total*100):0)+"%";
    if(curEl){curEl.textContent=p.current||"—";curEl.className="bk-prog-cur";}
  }else if(bkPhase==="scanning"){
    if(countEl)countEl.textContent="";
    if(fillEl)fillEl.style.width="100%";
    if(curEl){curEl.textContent="正在扫描目录…";curEl.className="bk-prog-cur";}
  }else if(bkPhase==="done"){
    if(countEl)countEl.textContent="";
    if(fillEl)fillEl.style.width="100%";
    if(curEl){curEl.textContent=p.message||"完成";curEl.className="bk-prog-cur ok";}
  }else if(bkPhase==="error"){
    if(countEl)countEl.textContent="";
    if(fillEl)fillEl.style.width="0%";
    if(curEl){curEl.textContent=p.message||"出错";curEl.className="bk-prog-cur err";}
  }
  // 备份结束（done/error）后刷新历史列表，确保最新一条入库后立即可见
  if(bkPhase==="done"||bkPhase==="error"){setTimeout(loadBackupHistory,800);}
};

/* ========= 下载任务管理（DDM3U8，通过能力路由 /portal/api/download/*） =========
 * DDM3U8 状态用中文（排队中/下载中/合并中/转换中/已暂停/已取消/已完成/失败），
 * 前端映射成 5 类徽章：queued/running/paused/done/error 便于统一视觉。
 * 所有请求经中间件反代，Basic Auth 由适配器注入，前端只走 cookie 会话。
 */
// DDM3U8 中文状态 → 徽章类 + 简称
function dlStatusBadge(s){
  s=String(s||"");
  if(s==="排队中"||s==="等待FFmpeg")return ["queued","排队"];
  if(s==="下载中"||s==="合并中"||s==="转换中")return ["running",s];
  if(s==="已暂停")return ["paused","暂停"];
  if(s==="已完成")return ["done","完成"];
  if(s==="失败"||s==="已取消")return ["error",s==="已取消"?"取消":"失败"];
  return ["",s];
}
function loadDownloadTasks(){
  const listEl=document.getElementById("dl-list");
  const activeEl=document.getElementById("dl-active");
  const maxEl=document.getElementById("dl-max");
  if(!listEl)return; // 不在 download 页
  listEl.innerHTML='<div class="dl-empty"><span class="spin"></span>加载中…</div>';
  fetch("/portal/api/download/tasks").then(r=>{
    if(!r.ok)throw new Error("HTTP "+r.status+(r.status===404?"（下载适配器未启用）":""));
    return r.json();
  }).then(resp=>{
    if(activeEl)activeEl.textContent=resp.active_workers||0;
    if(maxEl)maxEl.textContent=resp.max_workers||0;
    // tasks 是对象 {id: task}，task_order 是 id 数组（按 created_at 倒序）
    const order=resp.task_order||[];
    const tasks=resp.tasks||{};
    if(!order.length){listEl.innerHTML='<div class="dl-empty">暂无下载任务</div>';return;}
    listEl.innerHTML=order.map(function(tid){
      const t=tasks[tid]||{};
      const name=esc(t.name||tid);
      const [bc,bl]=dlStatusBadge(t.status);
      const ct=fmtBackupTime(t.created_at?Date.parse(t.created_at):0);
      const log=t.log?('<div class="dl-task-log">'+esc(t.log)+'</div>'):'';
      // 操作按钮：按状态显示可用动作
      const st=t.status;
      let acts='<div class="dl-actions">';
      if(st==="下载中"||st==="合并中"||st==="转换中")acts+='<button class="dl-act" onclick="taskAction(\''+esc(tid)+'\',\'pause\')">暂停</button>';
      if(st==="已暂停")acts+='<button class="dl-act" onclick="taskAction(\''+esc(tid)+'\',\'resume\')">恢复</button>';
      if(st==="下载中"||st==="合并中"||st==="转换中"||st==="已暂停"||st==="排队中")acts+='<button class="dl-act danger" onclick="taskAction(\''+esc(tid)+'\',\'cancel\')">取消</button>';
      if(st!=="下载中"&&st!=="合并中"&&st!=="转换中"&&st!=="排队中")acts+='<button class="dl-act danger" onclick="taskAction(\''+esc(tid)+'\',\'cancel\')">删除</button>';
      acts+='</div>';
      return '<div class="dl-task"><div class="dl-task-head"><span class="dl-task-name">'+name+'</span><span class="dl-badge '+bc+'">'+esc(bl)+'</span></div><div class="dl-task-meta"><span>'+ct+'</span></div>'+log+acts+'</div>';
    }).join("");
  }).catch(e=>{
    listEl.innerHTML='<div class="dl-empty">加载失败：'+esc(e.message)+'</div>';
  });
}
// 提交下载：构造 form-data，DDM3U8 /down 接口要求表单字段而非 JSON
function submitDownload(){
  const urlEl=document.getElementById("dl-url");
  const nameEl=document.getElementById("dl-name");
  const refEl=document.getElementById("dl-referer");
  const subEl=document.getElementById("dl-subpath");
  const goBtn=document.getElementById("dl-go");
  if(!urlEl)return;
  const url=urlEl.value.trim();
  if(!url){toast("请粘贴 m3u8 链接");urlEl.focus();return;}
  if(goBtn){goBtn.disabled=true;goBtn.textContent="提交中…";}
  const fd=new FormData();
  fd.append("url",url);
  fd.append("name",nameEl?nameEl.value.trim():"video");
  if(refEl&&refEl.value.trim())fd.append("referer",refEl.value.trim());
  if(subEl&&subEl.value.trim())fd.append("sub_path",subEl.value.trim());
  fetch("/portal/api/download/submit",{method:"POST",body:fd}).then(r=>{
    return r.json().then(j=>({ok:r.ok,j}));
  }).then(({ok,j})=>{
    if(goBtn){goBtn.disabled=false;goBtn.textContent="提交下载";}
    if(ok){
      toast("提交成功："+(j.message||"已创建任务"),1800);
      if(urlEl)urlEl.value="";
      if(nameEl)nameEl.value="";
      if(refEl)refEl.value="";
      if(subEl)subEl.value="";
      setTimeout(loadDownloadTasks,300);
    }else{
      toast("提交失败："+(j.error||"未知错误"),3000);
    }
  }).catch(e=>{
    if(goBtn){goBtn.disabled=false;goBtn.textContent="提交下载";}
    toast("提交失败："+e.message,3000);
  });
}
// 任务操作：pause/resume/cancel/merge，POST JSON {action:...} 到 /download/task/<id>
function taskAction(tid,action){
  if(!tid)return;
  const tip={pause:"暂停",resume:"恢复",cancel:"取消",merge:"合并"}[action]||action;
  fetch("/portal/api/download/task/"+encodeURIComponent(tid),{
    method:"POST",headers:{"Content-Type":"application/json"},
    body:JSON.stringify({action:action})
  }).then(r=>{
    if(r.ok){toast(tip+" 已执行",1000);setTimeout(loadDownloadTasks,400);}
    else{toast(tip+" 失败：HTTP "+r.status,2000);}
  }).catch(e=>toast(tip+" 失败："+e.message,2000));
}

/* ========= 原生系统返回/侧滑返回 转发处理（避免滑动就退桌面） =========
 * 由 MainActivity.onBackPressed 回调触发，同步返回字符串状态：
 *   handled     — 已在 H5 内部消费（文件页 goUp / 切回首页）
 *   not_handled — H5 已在首页顶层，原生应执行 finish() 退出
 */
window.__onNativeBack=function(){
  // 1) 文件页：非根 → 上一级；根 → 切回首页
  if(curTab==="files"){
    if(curFiles){
      goUp();
      return "handled";
    }
    setTab("home");
    return "handled";
  }
  // 2) 下载页 / 我的页 → 切回首页
  if(curTab==="download"||curTab==="me"){
    setTab("home");
    return "handled";
  }
  // 3) 首页 → 交给原生 finish() 退出
  return "not_handled";
};

/* ========= 远程目录浏览（复用能力路由 /portal/api/files/list） ========= */
// 备份模块内嵌的目录选择器：点击"浏览"按钮弹出模态窗，
// 在 OpenList/AList 远端目录树中导航并选定远程备份根路径。
// 设计目标：用户能直观看到"备份到 OpenList 的哪个文件夹下"。
var browseCur="";       // 当前浏览的远程路径（相对，如 "手机备份/照片"）
function browseRemoteDir(){
  const root=document.getElementById("bk-browse");
  const listEl=document.getElementById("bk-browse-list");
  if(!root||!listEl)return;
  // 初始路径：取当前远程路径输入框的值，去掉首尾 "/"
  const remoteEl=document.getElementById("bk-remote");
  let init="";
  if(remoteEl){init=remoteEl.value.trim().replace(/^\/+/,"").replace(/\/+$/,"");}
  root.style.display="flex";
  // 诊断：WebView 内 card 实际渲染尺寸/位置，从 Logcat 看（tag DDNAS-Portal）
  // 若 width/height=0 说明 flex 计算失败，card 被压扁
  try{
    setTimeout(()=>{
      const card=document.querySelector(".bk-browse-card");
      if(card){
        const r=card.getBoundingClientRect();
        const cs=getComputedStyle(card);
        const log="browse-card rect: w="+Math.round(r.width)+" h="+Math.round(r.height)+" top="+Math.round(r.top)+" left="+Math.round(r.left)+" bg="+cs.background+" disp="+cs.display+" vis="+cs.visibility+" flex="+cs.flexDirection;
        try{if(typeof ddnas!=="undefined"&&ddnas&&ddnas.log)ddnas.log(log);else console.log("[browse-diag] "+log);}catch(e){console.log("[browse-diag-err] "+log);}
      }
    },50);
  }catch(e){}
  browseLoad(init);
}
function closeBrowse(){
  const root=document.getElementById("bk-browse");
  if(root)root.style.display="none";
}
function browseLoad(p){
  browseCur=p||"";
  const pathEl=document.getElementById("bk-browse-path");
  const listEl=document.getElementById("bk-browse-list");
  if(!pathEl||!listEl)return;
  pathEl.textContent="/"+browseCur;
  listEl.innerHTML='<div class="bk-browse-empty">加载中…</div>';
  // AbortController + 超时：避免 WebView 内 fetch 卡死（Cloudflare/网络问题时一直 pending），
  // 超时后明确提示，便于用户排查（而不是永远"加载中…"）。
  const ctrl=new AbortController();
  const timer=setTimeout(()=>ctrl.abort(),15000);
  const url="/portal/api/files/list?path="+encodeURIComponent(browseCur);
  fetch(url,{credentials:"same-origin",signal:ctrl.signal}).then(r=>{
    if(!r.ok){
      // 401：cookie 失效，提示用户回首页触发重新登录
      const hint=r.status===401?"（登录已失效，请下拉刷新或重新打开页面）":"";
      throw new Error("HTTP "+r.status+hint);
    }
    return r.json();
  }).then(resp=>{
    clearTimeout(timer);
    if(resp.error)throw new Error(resp.error);
    const items=(resp.items||[]).filter(it=>{
      // 兼容 is_dir 是 boolean 或 0/1 数字（AList 不同版本可能两种形式）
      const isDir=!!(it.is_dir||it.is_dir===1||it.type==="folder");
      return isDir;  // 仅展示目录，文件不参与路径选择
    }).sort((a,b)=>String(a.name||"").localeCompare(String(b.name||"")));
    if(!items.length){
      listEl.innerHTML='<div class="bk-browse-empty">空目录</div>';
      return;
    }
    listEl.innerHTML=items.map(it=>{
      const name=esc(it.name||"");
      const rel=joinPath(browseCur,it.name||"");
      return '<div class="bk-browse-item" data-rel="'+esc(rel)+'">'+
        '<span class="ic">📂</span><span class="nm">'+name+'</span><span class="arr">›</span></div>';
    }).join("");
    listEl.querySelectorAll(".bk-browse-item").forEach(el=>{
      el.addEventListener("click",()=>browseLoad(el.dataset.rel||""));
    });
  }).catch(e=>{
    clearTimeout(timer);
    // AbortError 单独标识：网络未在 15s 内响应（典型 Cloudflare 卡死）
    const msg=e&&e.name==="AbortError"?"加载超时（15s 内无响应，可能网络受限或代理拦截）":e.message;
    // 通过 ddnas 桥输出到 Logcat（App WebView 内可用）
    try{if(typeof ddnas!=="undefined"&&ddnas&&ddnas.log)ddnas.log("[browse] "+url+" -> "+msg);}catch(_){}
    listEl.innerHTML='<div class="bk-browse-empty">加载失败：'+esc(msg)+'<br><a href="/admin/adapter/openlist" style="color:var(--accent)">前往配置 -></a></div>';
  });
}
function browseUp(){
  if(!browseCur)return;
  const i=browseCur.lastIndexOf("/");
  browseLoad(i<0?"":browseCur.slice(0,i));
}
function browsePick(){
  const remoteEl=document.getElementById("bk-remote");
  if(!remoteEl){closeBrowse();return;}
  // 规范：以 / 开头，以 / 结尾
  let base="/"+browseCur;
  if(base.charAt(base.length-1)!=="/")base=base+"/";
  remoteEl.value=base;
  remoteEl.dataset.touched="1";
  closeBrowse();
  // 直接保存，避免用户还要手动点"保存"
  saveRemoteBase();
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
  const usedEl=document.getElementById("d-used");
  const totalEl=document.getElementById("d-total");
  const freeEl=document.getElementById("d-free");
  const barEl=document.getElementById("d-usage-bar");
  const descEl=document.getElementById("d-desc");
  if(!s){
    modelEl.textContent=lanIP();
    usedEl.textContent="-";totalEl.textContent="-";freeEl.textContent="-";barEl.style.width="0%";
    descEl.textContent="启用并配置 node 适配器后显示设备信息";
    return;
  }
  // d-model 显示当前访问中间件用的 IP（即 NAS 在局域网的入口地址）
  modelEl.textContent=lanIP();
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
  const netDev=net.device==="__sum__"?"全部网卡":(net.device||"—");
  const netHtml=mCardHTML({title:"网络",bar:false,
    right:"↓"+fmtBytes(+net.rx_rate||0)+"/s ↑"+fmtBytes(+net.tx_rate||0)+"/s",
    metrics:[
      {k:"累计接收",v:fmtBytes(+net.rx_bytes||0)},
      {k:"累计发送",v:fmtBytes(+net.tx_bytes||0)},
      {k:"网卡",v:esc(netDev)}
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
  if(upBtn){
    // 根目录禁用上传：OpenList 根目录通常是只读挂载（挂的是其他盘的根，无写入空间），
    // 用户必须先进入子目录才能上传，避免所有文件静默失败。
    if(curFiles){
      upBtn.disabled=false;
      upBtn.title="上传到当前目录: /"+curFiles;
    }else{
      upBtn.disabled=true;
      upBtn.title="根目录不支持上传，请先进入子目录";
    }
  }
  renderCrumb(curFiles);
  body.innerHTML='<div class="loading"><span class="spin"></span>加载中…</div>';
  fetch("/portal/api/files/list?path="+encodeURIComponent(curFiles)).then(r=>{
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
  const url=location.origin+"/portal/api/files/stream/"+relPath.split("/").map(encodeURIComponent).join("/");
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
  const url=location.origin+"/portal/api/files/stream/"+relPath.split("/").map(encodeURIComponent).join("/");
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
  const url=location.origin+"/portal/api/files/stream/"+relPath.split("/").map(encodeURIComponent).join("/");
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
  // 防御性 guard：按钮虽 disabled，仍拦截绕过（F12 改属性等），根目录直接拒收
  if(!dir){toast("根目录不支持上传，请先进入子目录",2500);input.value="";return;}
  const dirDisp="/"+dir;
  let ok=0,fail=0;const total=files.length;
  for(let i=0;i<files.length;i++){
    const f=files[i];
    const dest=joinPath(dir,f.name);
    toast("上传中 ("+(i+1)+"/"+total+") → "+dirDisp+"/"+f.name);
    try{
      const r=await fetch("/portal/api/files/upload?path="+encodeURIComponent(dest),{method:"POST",body:f,headers:{"Content-Type":"application/octet-stream"}});
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
