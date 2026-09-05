# 音频播放器（方案 B：复用 OpenList stream，自建轻量播放器）

## 问题

DDNAS portal 文件管理中，点击音频文件（mp3/flac/m4a 等）目前走 `playMedia` → 全屏视频播放器（PlayerActivity / HTML5 video），体验差：
- 全屏占用，无法边浏览边听
- 没有播放列表，播完一首就停
- App 端无法后台播放（息屏/切后台即中断）

用户希望有一个"音乐播放器"形态：点击某音频后，自动把**同目录其他音频**组成播放列表，连续播放；App 端支持后台播放。

## 方案选择

用户音乐主要挂在 OpenList（云盘），现成 Docker 音乐播放器（Navidrome 等）需要直接文件系统访问、不能通过 OpenList API 读云盘文件，故采用方案 B：
- Web 端：HTML5 `<audio>` + 同目录播放列表，迷你播放器 UI（非全屏）
- App 端：`MusicService` 前台服务 + ExoPlayer，息屏后台连续播放
- 音频流统一走 OpenList 已有的 `/portal/api/files/stream/{path}` 代理（支持 Range、cookie 鉴权）

## 用户与目标

- 主用户：DDNAS portal（Web 浏览器 + App WebView 套壳）使用者
- 目标：在文件管理页点任意音频，弹出迷你播放器，自动加载同目录所有音频为列表，可上一首/下一首/暂停/进度拖动/循环；App 端息屏后仍连续播放

## 非目标

- 不做音乐库扫描/标签管理（按文件夹组织，不读 ID3 艺术家/专辑）
- 不做专辑封面/歌词（首版只显示文件名）
- 不做转码（直传源文件流）
- 不做播放列表持久化（每次点击即时生成）

## 功能需求

### FR-1 Web 端音频点击分流
- 点击音频文件的"播放"按钮时，**不再**调 `playMedia`（全屏播放器），改为打开迷你音乐播放器
- 视频文件仍走原 `playMedia` 全屏逻辑
- 判定依据：`mediaExt(name)` 返回 `"audio"`

### FR-2 同目录播放列表自动生成
- 点击某音频后，播放器自动以该文件**所在目录**为范围，列出所有音频文件（按文件名排序）
- 当前点击的文件作为首曲播放
- 列表项显示文件名，可点击切换

### FR-3 Web 端迷你播放器 UI
- 固定底部的迷你播放器条（不占全屏，可关闭）
- 显示：当前歌名、播放/暂停、上一首、下一首、进度条（可拖动 seek）、循环模式（列表循环/单曲循环/顺序）
- 可展开/收起播放列表
- 浏览器环境用 HTML5 `<audio>`，直接播放 stream URL（同源 cookie 自动带）

### FR-4 App 端后台播放
- App WebView 内点音频播放时，优先调原生桥 `ddnas.playMusic` 而非 HTML5 audio
- 新建 `MusicService`（前台服务，`foregroundServiceType=mediaPlayback`），用 ExoPlayer 播放
- 播放列表由 Web 端通过 JS 桥传入（JSON 数组：`[{name, url}]`）
- 支持上一首/下一首/暂停/恢复/进度 seek
- 一首播完自动下一首（列表循环）
- 息屏/切后台后持续播放

### FR-5 前台通知与媒体控制
- `MusicService` 启动后显示前台通知（含歌曲名、播放/暂停、上一首、下一首按钮）
- 通知按钮可控制播放
- 锁屏媒体控制（MediaSession）

### FR-6 系统锁与稳定性
- 播放期间持有 `WakeLock`（PARTIAL_WAKE_LOCK）和 `WifiLock`，避免息屏 Doze 中断
- 锁释放时机：真正停止播放（用户停止/列表结束）时才释放，"播完→自动下一首"间隙持锁不释放（避免 Doze 延迟导致断播）

### FR-7 Cookie 注入
- ExoPlayer 通过 OkHttpDataSource 请求 stream URL，注入 WebView 登录后的 admin 会话 cookie（与 PlayerActivity 同机制）

## 非功能需求

- NFR-1：Web 端播放器不阻塞页面其他操作（底部条，可关闭）
- NFR-2：App 端 MusicService 不随 Activity 销毁而停止
- NFR-3：播放列表生成不额外发请求（复用文件列表页已有的数据，或按需 list 当前目录）
- NFR-4：播放器状态变化通过 JS 回调通知前端更新 UI（播放/暂停/进度/切歌）

## 约束与依赖

- 复用 OpenList `/portal/api/files/stream/{path}`（已支持 Range、cookie 鉴权、无整体超时）
- 复用 Android media3 ExoPlayer（已引入 `media3-exoplayer:1.3.1`、`media3-datasource-okhttp`）
- 复用 DdnasApplication.okHttpClient（含浏览器 UA、超时配置）
- Android 14+ 前台服务需声明 `FOREGROUND_SERVICE_MEDIA_PLAYBACK` 权限

## 假设

- 音频文件通过 OpenList stream API 可正常获取（与视频播放同链路，已知可用）
- App WebView 与中间件同源，cookie 可通过 CookieManager 获取
- 用户授予通知权限（POST_NOTIFICATIONS，备份功能已申请）

## 验收标准

### AC-1（rule）：Web 端点击音频打开迷你播放器，非全屏视频
- 在 portal 文件管理页点击音频文件"播放"按钮，页面底部出现迷你播放器条，**不**弹出全屏视频播放器
- 视频文件点击仍走全屏播放

### AC-2（rule）：同目录音频组成播放列表
- 点击 `/音乐/周杰伦/晴天.mp3`，播放器列表自动包含 `/音乐/周杰伦/` 下所有音频文件，首曲为晴天.mp3

### AC-3（rule）：Web 端播放控制可用
- 播放/暂停、上一首、下一首、进度拖动 seek 均正常响应
- 一首播完自动跳下一首

### AC-4（rule）：App 端后台播放
- App 内点音频播放后，按 Home 键/息屏，音乐**不中断**，继续播放
- 一首播完自动下一首

### AC-5（rule）：前台通知控制
- App 播放音乐时，通知栏显示含歌曲名的通知，有播放/暂停、上一首、下一首按钮，点击可控制

### AC-6（rule）：Cookie 注入正确
- App 端播放音频流返回 200/206，非 401（cookie 注入成功）

### AC-7（rubric）：Web 端播放器 UI 体验
- 维度：迷你播放器不遮挡主要内容、控件齐全、可关闭
- 0=遮挡严重/控件缺失；1=基本可用但有瑕疵；2=体验良好，不影响浏览
- 通过阈值：≥1
