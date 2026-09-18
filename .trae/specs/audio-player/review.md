# Review：音频播放器（方案 B）

## 审查范围
- portal.go（Web 端播放器 UI + JS）
- MusicService.kt（Android 前台服务 + ExoPlayer）
- MainActivity.kt（JS 桥）
- AndroidManifest.xml、DdnasApplication.kt、build.gradle.kts

## 检查点

### AC-1：Web 端音频点击分流（rule）
- [x] `play(relPath)` 中 `if(mediaExt(relPath)==="audio"){playAudio(relPath);return;}` 正确分流
- [x] video 类型保持原 `playMedia` → PlayerActivity / openVideoPlayer
- 证据：portal.go:1467-1480

### AC-2：同目录播放列表（rule）
- [x] `playAudio` 从 `curItems` 筛选 `mediaExt==="audio"` 项，生成 `[{name,rel,url}]`
- [x] `findIndex(a=>a.rel===relPath)` 定位点击项为首曲
- 证据：portal.go:1533-1541

### AC-3：Web 端播放控制（rule）
- [x] musicToggle / musicPrev / musicNext / musicSeek / musicPlayAt 均实现
- [x] `musicAudio.addEventListener("ended", musicOnEnded)` 播完自动下一首
- [x] loopMode 支持 list（循环）/ one（单曲）/ order（顺序）
- 证据：portal.go:1574-1612

### AC-4：App 端后台播放（rule）
- [x] MusicService 前台服务 + ExoPlayer，`foregroundServiceType=mediaPlayback`
- [x] `WakeLock(PARTIAL_WAKE_LOCK)` + `WifiLock(WIFI_MODE_FULL_HIGH_PERF)` 持锁
- [x] 锁释放时机：`stopMusic` 时 release，自然播完不释放（覆盖续播间隙）
- [x] `REPEAT_MODE_ALL` 列表循环
- 证据：MusicService.kt acquireLocks/releaseLocks/stopMusic

### AC-5：前台通知控制（rule）
- [x] NotificationCompat + MediaSession + MediaStyle
- [x] 含 上一首/播放暂停/下一首/关闭 action，`setShowActionsInCompactView(1,2)`
- [x] MediaSession.Callback 处理 play/pause/next/prev/stop/seekTo
- 证据：MusicService.kt buildNotification/initMediaSession

### AC-6：Cookie 注入（rule）
- [x] buildAuthedClient 复用 PlayerActivity 逻辑：对 originHost 注入 Cookie 头
- [x] OkHttpDataSource.Factory(client) 让 ExoPlayer Range 请求携带 cookie
- 证据：MusicService.kt buildAuthedClient

### AC-7：Web 端 UI 体验（rubric）
- 评分：1（基本可用）
- 理由：底部固定条不占全屏，有歌名/控制/进度/列表/关闭；但循环模式切换 UI 未暴露（默认 list 循环），封面/歌词缺失（非目标）
- 证据：portal.go CSS .music-player + DOM #music-player

## 发现的问题与风险

### F-1（minor）：MusicService 静态 play() 轮询无超时
- `MusicService.play` 用 Handler 每 50ms 轮询 `instance`，若 Service 启动失败会无限轮询
- 影响：低（Service 启动失败极少）；建议后续加超时（如 2s 后 toast 提示）
- 不阻塞验收

### F-2（minor）：通知图标用系统 drawable
- `setSmallIcon(android.R.drawable.ic_media_play)` 系统图标，部分设备可能显示为白色方块
- 影响：低；建议后续替换为 app 自有图标
- 不阻塞验收

### F-3（note）：loopMode 切换未暴露 UI
- JS 端有 list/one/order 逻辑，但无 UI 按钮切换，默认 list 循环
- 影响：低（列表循环符合主需求）；后续可加切换按钮

## 结论

**pass** — 所有 AC 的 rule 检查点通过，rubric AC-7 达阈值（≥1）。F-1/F-2/F-3 均为 minor/note，不阻塞验收，可后续迭代优化。

## 验证状态
- 中间件 go build 通过 ✅
- App 端无法本地编译（Gradle wrapper jar 缺失），代码语法经人工审查
- 待 GitHub Actions 构建验证 + 真机测试
