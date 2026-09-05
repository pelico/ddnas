# DDNAS

通过 GitHub 构建 **Android App** + **Docker 中间件**的轻量 NAS 管理方案。Docker 容器作为聚合中间站，把 OpenList/AList、node_exporter、DDM3U8 等上游接口汇集为统一 API；App 以 WebView 套壳加载中间件 `/portal` 页面，通过 JS 桥接原生播放、备份等能力。

中间件采用**插件化架构**：每个上游是一个实现了 `Adapter` 接口的适配器，声明自身能力、配置表单 schema 与路由；新增上游只需加一个适配器包并在 `init()` 注册，路由与 Admin 配置表单自动接入，核心无需改动。

## 功能概览

| 模块 | 说明 |
|---|---|
| 🗂️ 文件管理 | 目录浏览、上传、新建目录、流式播放（视频/音频/图片），基于 OpenList/AList 适配器 |
| 🎵 音乐播放 | 点击音频弹出底部迷你播放器，自动把**同目录所有音频**组成播放列表；App 端 `MusicService` 前台服务实现**息屏后台连续播放**、通知栏/锁屏控制 |
| 💾 手机备份 | SAF 选择本地目录，增量上传到远程路径；支持充电+Wi-Fi 自动备份（WorkManager）；备份历史持久化在中间件 SQLite，可查看失败文件、重试、删除 |
| ⬇️ 下载任务 | 对接 DDM3U8（独立容器），提交 m3u8 链接下载，实时查看任务状态/日志，支持暂停/恢复/取消/合并 |
| 🖥️ 设备监控 | CPU / 内存 / 网络 / 硬盘，10s 轮询刷新，来自 node_exporter |
| ⚙️ 配置控制台 | 首次设置向导、各适配器配置表单（含「测试连接」）、App 连接信息；保存即热重载 |

## 架构

```
┌────────────┐   cookie 会话    ┌──────────────────────────┐   HTTP   ┌──────────┐
│ Android App│ ───────────────▶ │ Docker 中间件 (Go)       │ ───────▶ │ OpenList │
│ (Kotlin)   │ ◀─── 统一 JSON ─ │  - Admin Web UI(配置)     │          ├──────────┤
│  WebView   │                  │  - /portal SPA(功能页)    │ ───────▶ │node_exporter│
│  +JS 桥    │                  │  - 插件 Adapter 注册表    │          ├──────────┤
└────────────┘                  │  - 双鉴权(Bearer+Cookie)  │ ───────▶ │ DDM3U8   │
                                │  - 热重载(保存即生效)     │          └──────────┘
                                └────────────┬─────────────┘
                                             │ 卷映射 /data (config.yaml + backup.db)
```

**Web/Android 分工**：UI 全部在 Docker Web 端实现（`/portal` SPA），App 端仅通过 `ddnas` JS 桥接原生能力——`playMedia`（全屏视频）、`playMusic`/`musicControl`（后台音乐）、`startBackup`/`chooseBackupDir`（备份）、`viewImage`/`downloadFile`。所有数据请求走同源 `/portal/api/*`，由 Go 后端反代到内网适配器，客户端永远只访问中间件 `:8080`，无跨域。

## 双鉴权模型

- **Bearer Token**（`/api/*`）：供外部程序化访问，令牌在「App 连接」页生成。
- **Cookie 会话**（`/portal` 与 `/portal/api/*`）：供 App WebView 与浏览器访问，登录 `/admin/login` 后下发 `ddnas_admin` cookie（12h TTL，HttpOnly，SameSite=Lax）。
- 登录/设置成功后默认跳转到 `/portal`（App 套壳首页）；若 URL 带 `?redirect=` 且为本站内合法相对路径，则回跳原请求页。`redirectTarget` 会拒绝 `//`、协议、反斜杠等危险字符，防止开放重定向。
- `/portal` 前端 monkey-patch `fetch`，收到 401 自动跳转登录页，避免停留在 JSON 错误响应上。

## 音乐播放器（方案 B）

音乐文件挂在 OpenList 云盘，现成 Docker 音乐播放器（Navidrome 等）需要直接文件系统访问、无法通过 OpenList API 读云盘，故自建轻量播放器：

- **Web 端**：HTML5 `<audio>` + 同目录播放列表，底部固定迷你播放器条（可关闭），支持播放/暂停、上一首/下一首、进度拖动 seek、列表循环。
- **App 端**：`MusicService` 前台服务（`foregroundServiceType=mediaPlayback`）+ ExoPlayer，息屏/切后台后连续播放。
  - 播放列表由 Web 端通过 `ddnas.playMusic(index, playlistJson)` 传入（`[{name, url}]`）。
  - 音频流复用 OpenList `/portal/api/files/stream/{path}`（支持 Range、cookie 鉴权），ExoPlayer 经 OkHttp 注入 WebView 的 admin 会话 cookie。
  - `MediaSessionCompat` 支持锁屏媒体控制；前台通知含歌曲名与上一首/播放暂停/下一首/关闭按钮。
  - 播放期间持有 `WakeLock`（PARTIAL）+ `WifiLock`，真正停止时才释放，避免 Doze 断播。
  - 播放状态通过 `onMusicStateChange(json)` JS 回调通知前端更新 UI。

## 仓库结构

```
middleware/        Go 中间件（CGO 禁用静态二进制）
  cmd/ddnas/       入口
  internal/        config / plugin / server / admin / store
  plugins/         openlist / nodeexporter / ddm3u8 / downloader(占位)
  Dockerfile       多架构构建（amd64/arm64/armv7）
app/               Kotlin + Compose Android App
  MainActivity.kt  WebView 套壳 + ddnas JS 桥
  MusicService.kt  音乐前台服务（ExoPlayer + MediaSession）
  BackupService.kt / BackupWorker.kt  增量备份 + 自动备份
.github/workflows/ docker.yml(镜像) + android.yml(APK)
```

## 部署中间件（一次配置，永久生效）

```bash
# armv7l / arm64 / amd64 通用，多架构镜像由 CI 推送到 GHCR
docker run -d --name ddnas \
  -p 8080:8080 \
  -v /your/local/path:/data \
  --restart unless-stopped \
  ghcr.io/pelico/ddnas:latest
```

- 浏览器访问 `http://<宿主IP>:8080/`，首次进入**设置向导**：设管理员账号、生成 App 令牌，完成后自动跳转 `/portal`。
- 登录后在「适配器总览」逐个启用上游：填 OpenList 地址/令牌、node_exporter 地址、DDM3U8 地址等，**保存即热重载**，无需重启容器。
- 所有配置写入 `/data/config.yaml`，备份历史写入 `/data/backup.db`，容器重建不丢失。
- 「App 连接」页展示服务器地址 + App 令牌，填入 App 即可。

> 想用 `docker compose`？把上面 `-v /your/local/path:/data` 这条卷映射保留即可，配置全在卷里。

## App

App 填入服务器地址与 App 令牌，WebView 加载 `/portal`，底部三栏：**首页**（NAS 卡片 + 功能宫格 + 设备监控）、**文件**（列表/上传/播放/下载）、**我的**（备份面板 + 备份历史 + 控制台入口）。

## API 概览

外部程序化接口需 `Authorization: Bearer <app_token>`（`/api/*`）；App WebView 同源接口走 cookie 会话（`/portal/api/*`，是 `/api/*` 的镜像）。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/health` | 健康检查 |
| GET | `/api/adapters` | 适配器发现（能力+路由） |
| GET | `/api/node/system` | 结构化设备信息 |
| GET | `/api/node/metrics` | 原始 metrics 透传 |
| GET | `/api/files/list?path=` | 目录列表 |
| GET | `/api/files/get?path=` | 文件详情 |
| GET | `/api/files/stream/{path}` | 流式播放代理（含 Range） |
| POST | `/api/files/upload?path=` | 上传（请求体为文件字节） |
| POST | `/api/files/mkdir?path=` | 新建目录 |
| GET | `/api/download/tasks` | 下载任务列表 |
| POST | `/api/download/submit` | 提交 m3u8 下载 |
| POST | `/portal/api/backup/history` | 上报备份历史（App 端） |
| GET | `/portal/api/backup/history?limit=` | 备份历史列表 |
| DELETE | `/portal/api/backup/history[/{id}]` | 清空 / 删除单条备份历史 |

> 能力路由：前端只依赖能力名（`files` / `download`），不耦合 adapter 名；同能力多 adapter 只取首个已启用实例。

## CI

- `docker.yml`：push 到 main 或打 `v*` tag 时，用 buildx 构建 `linux/amd64,linux/arm64,linux/arm/v7` 并推送 `ghcr.io/pelico/ddnas`。
- `android.yml`：构建 debug APK，打 tag 时附到 GitHub Release。

## 本地开发

```bash
# 中间件
cd middleware && go run ./cmd/ddnas   # 数据目录默认 /data，开发可设 DDNAS_DATA_DIR=./data

# 新增适配器：实现 plugin.Adapter 接口，在包 init() 调 plugin.Register，
# 并在 cmd/ddnas/main.go 用空导入引入该包。
```
