# DDNAS

通过 GitHub 构建 **Android App** + **Docker 中间件**的轻量 NAS 管理方案。Docker 容器作为聚合中间站，把 OpenList、node_exporter 等上游接口汇集为统一 API，App 连接容器即可查看设备信息、浏览/上传/播放/备份文件，后续可接入下载器等接口。

中间件采用**插件化架构**：每个上游是一个实现了 `Adapter` 接口的适配器，声明自身能力、配置表单 schema 与路由；新增上游只需加一个适配器包并在 `init()` 注册，路由与 Admin 配置表单自动接入，核心无需改动。

## 架构

```
┌────────────┐   Bearer Token   ┌──────────────────────────┐   HTTP   ┌──────────┐
│ Android App│ ───────────────▶ │ Docker 中间件 (Go)       │ ───────▶ │ OpenList │
│ (Kotlin)   │ ◀─── 统一 JSON ─ │  - Admin Web UI(配置)     │          ├──────────┤
└────────────┘                  │  - 插件 Adapter 注册表    │ ───────▶ │node_exporter│
                                │  - 统一鉴权 + 聚合 API    │          ├──────────┤
                                │  - 热重载(保存即生效)     │ ───────▶ │ 下载器…   │
                                └────────────┬─────────────┘          └──────────┘
                                             │ 卷映射 /data (config.yaml 持久化)
```

## 仓库结构

```
middleware/        Go 中间件（CGO 禁用静态二进制）
  cmd/ddnas/       入口
  internal/        config / plugin / server / admin
  plugins/         openlist / nodeexporter / downloader(占位)
  Dockerfile       多架构构建（amd64/arm64/armv7）
app/               Kotlin + Compose Android App
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

- 浏览器访问 `http://<宿主IP>:8080/`，首次进入**设置向导**：设管理员账号、生成 App 令牌。
- 登录后在「适配器总览」逐个启用上游：填 OpenList 地址/令牌、node_exporter 地址等，**保存即热重载**，无需重启容器。
- 所有配置写入 `/data/config.yaml`，容器重建不丢失（这就是「不用每次倒腾部署参数」）。
- 「App 连接」页展示服务器地址 + App 令牌，填入 App 即可。

> 想用 `docker compose`？把上面 `-v /your/local/path:/data` 这条卷映射保留即可，配置全在卷里。

## App

App 填入服务器地址与 App 令牌，三页：**设备信息**（CPU/内存/磁盘/网络/运行时长，来自 `/api/node/system`）、**文件**（列表/上传/流式播放，来自 `/api/openlist/*`）、**设置**。

## API 概览（均需 `Authorization: Bearer <app_token>`）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/health` | 健康检查 |
| GET | `/api/adapters` | 适配器发现（能力+路由） |
| GET | `/api/node/system` | 结构化设备信息 |
| GET | `/api/node/metrics` | 原始 metrics 透传 |
| GET | `/api/openlist/files/list?path=` | 目录列表 |
| GET | `/api/openlist/files/get?path=` | 文件详情 |
| GET | `/api/openlist/files/stream/{path}` | 流式播放代理（含 Range） |
| POST | `/api/openlist/files/upload?path=` | 上传（请求体为文件字节） |
| POST | `/api/openlist/files/mkdir?path=` | 新建目录 |

## CI

- `docker.yml`：push 到 main 或打 `v*` tag 时，用 buildx 构建 `linux/amd64,linux/arm64,linux/arm/v7` 并推送 `ghcr.io/pelico/ddnas`。
- `android.yml`：构建 debug APK，打 tag 时附到 GitHub Release。

## 本地开发

```bash
# 中间件
cd middleware && go run ./cmd/ddnas   # 数据目录默认 /data，开发可设 DDNAS_DATA_DIR=./data

# 新增适配器：实现 plugin.Adapter 接口，在包 init() 调 plugin.Register，并在 cmd/ddnas/main.go 用空导入引入该包
```
