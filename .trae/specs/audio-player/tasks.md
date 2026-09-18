# 实现任务：音频播放器（方案 B）

## Task 1：Web 端迷你播放器 UI 与播放列表逻辑（portal.go）

**优先级**：high
**对应 AC**：AC-1, AC-2, AC-3, AC-7
**Status**: completed

### 改动
- `portal.go` 新增 CSS：`.music-player`（底部固定条）、`.music-list`（播放列表面板）
- `portal.go` 新增播放器 DOM（#music-player + #music-list）
- `portal.go` 新增 JS：playAudio / startMusic / musicToggle / prev / next / seek / playAt / onEnded / onMusicStateChange
- `play(relPath)` 分流：audio→playAudio，video→原 playMedia
- 新增全局 curItems 存当前目录文件项，供筛选同目录音频

### 完成证据
- 中间件 go build 通过
- playAudio 从 curItems 筛选 mediaExt==="audio" 的项组成列表
- App 桥存在时调 ddnas.playMusic，否则 HTML5 audio
- 播完 onEnded 按 loopMode 自动下一首

---

## Task 2：Android MusicService 前台服务 + ExoPlayer 后台播放

**优先级**：high
**对应 AC**：AC-4, AC-5, AC-6
**Status**: completed

### 改动
- 新建 `MusicService.kt`
  - ExoPlayer + OkHttpDataSource.Factory 注入 cookie（复用 buildAuthedClient 逻辑）
  - REPEAT_MODE_ALL 列表循环
  - 前台通知 + MediaSession（play/pause/prev/next/stop action）
  - WakeLock(PARTIAL_WAKE_LOCK) + WifiLock(WIFI_MODE_FULL_HIGH_PERF)：播放 acquire，stop release
  - AudioFocus 处理
  - onPlayerError 自动跳下一首
  - onTaskRemoved 停止播放
- `AndroidManifest.xml`：FOREGROUND_SERVICE_MEDIA_PLAYBACK 权限 + 服务声明
- `DdnasApplication.kt`：音乐通知渠道 ddnas_music
- `build.gradle.kts`：androidx.media:media:1.7.0

### 完成证据
- MusicService.instance 单例 + stateCallback 状态回传
- 静态 play() 方法 startForegroundService 后轮询 instance 调用实例 play()

---

## Task 3：Android JS 桥 ddnas.playMusic + 状态回传

**优先级**：high
**对应 AC**：AC-4, AC-6, NFR-4
**Status**: completed

### 改动
- `MainActivity.kt` Bridge 新增：
  - playMusic(index, playlistJson)：解析 JSON，获取 origin/cookie，启动 MusicService
  - musicControl(action)：play/pause/next/prev/stop
  - musicPlayAt(index)、musicSeek(percent)、getMusicState()
  - 设置 MusicService.stateCallback → portalWebView.evaluateJavascript(onMusicStateChange)
- `portal.go`：定义 onMusicStateChange(json) 更新播放器 UI

### 完成证据
- Bridge 方法用 @JavascriptInterface 注解
- stateCallback 在 UI 线程 evaluateJavascript

---

## Task 4：联调与边界处理

**优先级**：medium
**对应 AC**：AC-1 ~ AC-6
**Status**: completed

### 改动
- 播放列表为空时 toast 提示
- MusicService.onPlayerError 自动跳下一首
- musicClose 调 ddnas.musicControl("stop") 停止 Service
- onTaskRemoved 划掉 App 时 stopMusic

### 完成证据
- 边界逻辑均已覆盖

---

## 依赖关系

- Task 1 可独立（Web 端先用 HTML5 audio 跑通）
- Task 2 独立（原生服务）
- Task 3 依赖 Task 2（桥调用 MusicService）
- Task 4 依赖 Task 1+2+3

