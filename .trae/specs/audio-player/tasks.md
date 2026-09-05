# 实现任务：音频播放器（方案 B）

## Task 1：Web 端迷你播放器 UI 与播放列表逻辑（portal.go）

**优先级**：high
**对应 AC**：AC-1, AC-2, AC-3, AC-7

### 改动
- `portal.go` 新增 CSS：`.music-player`（底部固定条）、`.music-list`（播放列表面板）
- `portal.go` 新增 JS 函数：
  - `playAudio(relPath)`：音频点击入口，取代原 `play()` 对 audio 的分支
    - 从当前文件列表（`curFiles`）筛出同目录所有音频，生成 `playlist=[{name,url,rel}]`
    - 找到点击项 index，调用 `startMusic(index, playlist)`
  - `startMusic(index, playlist)`：
    - 若 App 桥 `ddnas.playMusic` 存在，调原生后台播放
    - 否则用 HTML5 `<audio>` 播放
  - `musicPlay/pause/prev/next/seekTo`：播放控制
  - `musicOnEnded`：播完自动下一首（循环）
  - 播放器条渲染：歌名、控制按钮、进度条、列表展开/收起、关闭
- 修改 `play(relPath)`：audio 类型调 `playAudio`，video 类型保持原 `playMedia` 逻辑
- `mediaExt` 已支持音频扩展名，无需改

### 测试要求
- TR-1（rule）：点音频文件→底部出现播放器条，**不**弹全屏 video
- TR-2（rule）：播放列表 = 当前目录所有音频，首曲=点击项
- TR-3（rule）：上一首/下一首/暂停/seek 均生效；播完自动下一首
- TR-7（rubric）：播放器条固定底部、可关闭、不遮挡文件列表主体

---

## Task 2：Android MusicService 前台服务 + ExoPlayer 后台播放

**优先级**：high
**对应 AC**：AC-4, AC-5, AC-6

### 改动
- 新建 `app/app/src/main/java/io/github/pelico/ddnas/MusicService.kt`
  - `Service` 子类，`foregroundServiceType=mediaPlayback`
  - ExoPlayer 实例，`OkHttpDataSource.Factory` 注入 cookie（复用 PlayerActivity 的 `buildAuthedClient` 逻辑）
  - 播放列表 `List<MediaItem>`，当前 index
  - `Player.Listener.onPlaybackStateChanged`：播完自动下一首
  - 前台通知：`NotificationCompat` + `MediaSession`，含播放/暂停/上一首/下一首 action
  - `WakeLock`（PARTIAL_WAKE_LOCK）+ `WifiLock`（WIFI_MODE_FULL）：播放开始 acquire，真正停止时 release（不自然结束时 release）
  - 状态回调：通过 `LocalBroadcastManager` 或 `StateFlow` 通知 MainActivity 更新 WebView UI
  - 控制方法：`play(index, playlist)`、`pause()`、`resume()`、`next()`、`prev()`、`seekTo(pos)`、`stop()`
- `AndroidManifest.xml`：
  - 新增 `FOREGROUND_SERVICE_MEDIA_PLAYBACK` 权限
  - 声明 `<service android:name=".MusicService" android:foregroundServiceType="mediaPlayback" />`
- `DdnasApplication.kt`：新增音乐通知渠道 `music_channel`

### 测试要求
- TR-4（rule）：App 点播放后按 Home/息屏，音乐持续播放不中断
- TR-5（rule）：通知栏显示歌曲名 + 播放/暂停/上一首/下一首按钮，点击生效
- TR-6（rule）：ExoPlayer 请求 stream URL 返回 200/206，非 401
- TR-8（rule）：一首播完自动下一首，息屏状态下也能续播

---

## Task 3：Android JS 桥 ddnas.playMusic + 状态回传

**优先级**：high
**对应 AC**：AC-4, AC-6, NFR-4

### 改动
- `MainActivity.kt` Bridge 新增：
  - `@JavascriptInterface fun playMusic(index: Int, playlistJson: String)`：解析 JSON 数组 `[{name,url}]`，启动 MusicService 并播放
  - `@JavascriptInterface fun musicControl(action: String)`：`play|pause|next|prev|stop`，转发给 MusicService
  - `@JavascriptInterface fun musicSeek(posMs: Long)`：seek
  - `@JavascriptInterface fun getMusicState(): String`：返回当前状态 JSON `{playing, index, position, duration}`
- MusicService 播放状态变化时，通过 `evaluateJavascript` 回调 WebView：`window.onMusicStateChange(json)`
- `portal.go`：定义 `window.onMusicStateChange` 更新播放器 UI

### 测试要求
- TR-9（rule）：App 内 WebView 点音频，调 `ddnas.playMusic`，MusicService 启动并播放
- TR-10（rule）：前端 UI 随播放状态实时更新（播放/暂停/进度/切歌）

---

## Task 4：联调与边界处理

**优先级**：medium
**对应 AC**：AC-1 ~ AC-6

### 改动
- 播放列表为空时提示
- 播放出错时（401/网络）提示并自动跳下一首或停止
- Web 端关闭播放器时通知 App 停止 MusicService
- 退出 App 时 MusicService 自动 stopSelf（或保留后台播放由用户控制）

### 测试要求
- TR-11（rule）：流返回 401 时前端提示"登录已失效"
- TR-12（rule）：关闭播放器条时 App 端 MusicService 停止

---

## 依赖关系

- Task 1 可独立（Web 端先用 HTML5 audio 跑通）
- Task 2 独立（原生服务）
- Task 3 依赖 Task 2（桥调用 MusicService）
- Task 4 依赖 Task 1+2+3
