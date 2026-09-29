<div align="center">

# Horizon · 地平线磁力下载

**不限速磁力 / BT / HTTP 下载器 · Windows 客户端 + Go 云端离线下载服务**

[中文](#-中文文档) · [English](#-english-documentation)

![platform](https://img.shields.io/badge/platform-Windows%207%2B-0078D4)
![client](https://img.shields.io/badge/client-C%23%20WinForms-512BD4)
![server](https://img.shields.io/badge/server-Go%201.26-00ADD8)
![version](https://img.shields.io/badge/version-v1.3.1-6A5AEB)
![license](https://img.shields.io/badge/license-MIT-green)

</div>

---

> **⚠️ 免责声明 / Disclaimer**
>
> 本项目是**通用下载工具**，本身不提供、不托管、不分发任何受版权保护的内容。
> 种子检索功能只是查询第三方公开索引站点的公开元数据，与搜索引擎聚合结果性质相同。
>
> **请仅将本工具用于下载你有权获取的内容**——例如 Linux 发行版 ISO、公有领域作品、
> 创作共用（CC）授权素材，或你已获授权的商业内容。下载和分发受版权保护的作品
> 在多数司法辖区属违法行为，需自行承担全部法律责任。
>
> This is a **general-purpose download tool**. It does not host, provide, or distribute
> any copyrighted content. The torrent search feature queries public metadata from
> third-party public indexers, equivalent to a search-engine aggregator.
>
> **Use it only for content you are legally entitled to obtain.** Downloading or
> distributing copyrighted works without authorization is illegal in most
> jurisdictions, and you bear sole responsibility.

---

## 📖 中文文档

### 这是什么

一套完整的磁力下载方案，由两部分组成：

| 组件 | 技术栈 | 角色 |
|---|---|---|
| **MagDownloader** | C# / .NET Framework 4.8 / WinForms | 桌面客户端：搜索、下载、影视库、云端任务管理 |
| **HorizonServer** | Go 1.26 | 云端服务：BT 离线下载、多源种子检索、影视元数据代理、账号鉴权 |

两者可独立使用，也可配合。**客户端不再内置任何服务器地址**：装好就是纯本地下载器，
磁力 / BT / HTTP 下载开箱可用；把服务器地址填进「服务器设置」之后，才会启用影视库、
中文关键词检索、云端离线下载这三项需要服务端的功能。

### 功能特性

**客户端**

- **下载引擎内嵌**：aria2c 已打包进 exe，双击即用，无需安装任何环境
- **多协议支持**：磁力链接（`magnet:`）、BT 种子、HTTP / HTTPS 直链
- **多任务并行**：实时显示进度、速度、大小、做种/连接数，支持暂停、续传、删除
- **磁力在线预览**：先解析出磁力内的文件列表（名称 + 大小），勾选后再下载，不必整包拉取
- **视频在线播放**：自动识别任务内的视频文件并调用本机播放器
- **全屏模式**：`F11` 进入 / 退出，无边框沉浸式界面
- **种子搜索**：关键词检索多个公开索引源，中文关键词自动翻译后再搜，中英结果合并去重
- **影视库**：封面 / 片名 / 简介浏览，支持最新更新、分类、搜索、详情
- **云端下载**：把磁力交给服务器离线下载，完成后取回本地
- **服务器设置**：登录页与主界面状态栏均可配置云端地址，带「测试连接」并回显服务端版本
- **账号体系**：云端优先、不可达自动回退本地账密；图形验证码；连续 5 次错误锁定
- **记住密码**：用 Windows DPAPI 加密后落盘，密文与当前 Windows 账户绑定
- **当日免验证码**：成功登录一次后当天不再要求验证码
- **令牌自动续期**：access token 过期自动用 refresh token 换发并重试，不会中途报错

**服务端**

- **BT 离线下载**：完整的 DHT / PEX / Tracker 支持，可配置端口与 uTP
- **多源种子检索**：apibay 多分类并发 + torrents-csv 变形词补充 + 词元命中过滤 + 做种分档排序
- **中文自动翻译**：中文关键词先翻译再检索，结果同时返回原文名与中文名
- **影视元数据代理**：服务端统一抓取 + 内存缓存 + 熔断降级，客户端不直连内容源
- **封面代理**：白名单域名 + SSRF 防护 + WebP 自动转 JPEG（老 .NET 解不了 WebP）
- **鉴权**：bcrypt 密码哈希 + JWT（access 12 小时 / refresh 30 天）+ 密钥平滑轮换
- **登录保护**：账号 + IP 组合维度失败计数，5 次错误锁定 10 分钟
- **限流**：公开接口与封面接口独立限额，支持内存或 Redis 后端，Redis 故障自动降级
- **SSRF 防护**：连接时按校验过的 IP 直连，堵住 DNS rebinding
- **存储可换**：SQLite（默认，零依赖）或 PostgreSQL
- **可选增强**：Redis（分布式限流）、MinIO / S3（下载完成后自动上传）
- **审计日志**：登录、任务增删、封禁操作全程留痕
- **优雅退出**：`SIGTERM` 后等待后台任务收尾再退出

### 项目结构

```
.
├── MagDownloader/            # 桌面客户端（C# WinForms）
│   ├── Program.cs            # 入口、未处理异常兜底
│   ├── MainForm.cs           # 主窗口：下载列表 / 搜索 / 影视库 / 云端 / 全屏
│   ├── AppCore.cs            # 路径、日志、会话、PBKDF2 密码哈希、账号库
│   ├── LoginForm.cs          # 登录 / 注册 / 验证码
│   ├── LoginMemory.cs        # DPAPI 加密的密码记忆
│   ├── CaptchaPass.cs        # 当日免验证码状态
│   ├── CloudClient.cs        # 云端认证与 API、令牌自动续期
│   ├── MovieBrowse.cs        # 影视库浏览视图
│   ├── MovieMeta.cs          # 影视元数据与封面缓存
│   ├── TorrentSearch.cs      # 种子检索
│   ├── FilePreviewForm.cs    # 磁力文件勾选预览
│   ├── HistoryStore.cs       # 下载历史
│   ├── AccountForms.cs       # 账号管理界面
│   ├── Assets.cs / Captcha.cs# 图标与图形验证码绘制
│   └── build.ps1             # 编译脚本（自动内嵌 aria2c 与图标）
│
└── HorizonServer/            # 云端服务（Go）
    ├── main.go               # 入口、依赖装配、优雅退出
    ├── internal/
    │   ├── api/              # 路由、鉴权中间件、限流、种子与影视库接口
    │   ├── auth/             # bcrypt + JWT 签发校验
    │   ├── engine/           # BT 下载引擎封装
    │   ├── torsearch/        # 多源种子检索与中文翻译
    │   ├── dytt/             # 影视元数据抓取（熔断 + 缓存降级 + 封面代理）
    │   ├── guard/            # 磁力链接校验 + SSRF 防护
    │   ├── ratelimit/        # 内存 / Redis 限流器
    │   ├── store/            # SQLite / PostgreSQL 存储
    │   ├── cloud/            # S3 / MinIO 上传
    │   ├── tmdb/             # TMDb 元数据增强（可选）
    │   └── version/          # 版本号
    ├── deploy/               # 部署辅助配置
    ├── k8s/                  # Kubernetes 清单
    └── Dockerfile / docker-compose.yml
```

### 快速开始

#### 方式一：直接用客户端（最简单）

1. 从 [Releases](../../releases) 下载 `MagDownloader.exe`
2. 双击运行，无需安装
3. 首次使用点「注册」创建账号（第一个注册的用户是管理员）

**可选：连接云端服务**

不配也能正常下载。想用影视库 / 中文检索 / 云端离线下载时：

1. 点登录页底部的「服务器设置」（或主界面底部状态栏的「云端」）
2. 填入服务端地址，例如 `http://124.222.167.203:8080` 或 `https://your-domain.com`
3. 点「测试连接」确认能连上并核对版本号，再点「保存」

地址会写入 `data\cloud.cfg`，下次启动自动读回；留空保存即回到本地模式。

#### 方式二：从源码编译客户端

**前置要求**

- Windows 7 或更高版本
- .NET Framework 4.8（Win10/11 已内置）
- **aria2c.exe**——仓库不含此文件，需自行下载：
  从 [aria2 releases](https://github.com/aria2/aria2/releases) 取 Windows 64 位压缩包，
  解压出 `aria2c.exe`

**编译**

```powershell
cd MagDownloader

# 指定 aria2c.exe 位置（三选一）
$env:ARIA2_PATH = "D:\tools\aria2\aria2c.exe"   # 方式 1：直接给文件路径
$env:ARIA2_DIR  = "D:\tools\aria2"               # 方式 2：给目录，自动递归查找

# 方式 3：先运行一次旧版客户端，它会解包到
#         %LOCALAPPDATA%\MagDownloader\aria2c.exe，脚本会自动找到

powershell -ExecutionPolicy Bypass -File .\build.ps1
```

产物为 `MagDownloader.exe`，脚本会把 `aria2c.exe` 和图标一并内嵌，并校验资源完整性。

#### 部署服务端

**前置要求**：Go 1.26 或更高版本

```bash
cd HorizonServer

# 必填：JWT 签名密钥（生产环境务必换成随机长字符串）
export HORIZON_JWT_SECRET="$(openssl rand -hex 48)"

# 可选：启用云端鉴权后，客户端才能连上来
go build -o horizon-server .
./horizon-server -addr :8080 -dir ./downloads
```

首次启动会自动建库建表。验证：

```bash
curl -s http://127.0.0.1:8080/healthz
# {"service":"horizon-downloader","status":"ok","version":"1.3.0"}
```

**Docker 方式**

```bash
cp .env.example .env      # 改掉里面所有 change-me
docker compose up -d
```

`docker-compose.yml` 已编排 PostgreSQL、Redis、MinIO、Prometheus、Grafana 全套。

**交叉编译 Linux 二进制**

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags "-s -w -X horizon/internal/version.Version=1.3.0" \
  -o horizon-server-linux .
```

### 服务端配置

所有配置项都同时支持命令行参数与环境变量，环境变量优先。

| 命令行 | 环境变量 | 默认值 | 说明 |
|---|---|---|---|
| `-addr` | `HORIZON_ADDR` | `:8080` | HTTP 监听地址 |
| `-dir` | `HORIZON_DIR` | `./downloads` | 下载文件存放目录 |
| `-jwt-secret` | `HORIZON_JWT_SECRET` | 空（必填） | JWT 签名密钥 |
| `-db-type` | `HORIZON_DB_TYPE` | `sqlite` | `sqlite` 或 `postgres` |
| `-db` | `HORIZON_DB` | `file:horizon.db?...` | SQLite DSN |
| `-postgres-dsn` | `HORIZON_POSTGRES_DSN` | 空 | PostgreSQL DSN |
| `-listen-port` | `HORIZON_LISTEN_PORT` | `42069` | BT / DHT 监听端口（需放行 TCP+UDP）|
| `-dht` | `HORIZON_DHT_ENABLED` | `true` | 是否启用 DHT |
| `-utp` | `HORIZON_UTP_ENABLED` | `false` | 是否启用 uTP |
| `-redis-addr` | `HORIZON_REDIS_ADDR` | 空 | Redis 地址，留空用内存限流 |
| `-redis-password` | `HORIZON_REDIS_PASSWORD` | 空 | Redis 密码 |
| `-s3-endpoint` | `HORIZON_S3_ENDPOINT` | 空 | S3 / MinIO 地址，留空关闭上传 |
| `-s3-access-key` | `HORIZON_S3_ACCESS_KEY` | 空 | S3 Access Key |
| `-s3-secret-key` | `HORIZON_S3_SECRET_KEY` | 空 | S3 Secret Key |
| `-s3-bucket` | `HORIZON_S3_BUCKET` | `downloads` | S3 桶名 |
| `-s3-use-ssl` | `HORIZON_S3_USE_SSL` | `false` | S3 是否用 HTTPS |
| `-dytt` | `HORIZON_DYTT_ENABLED` | `true` | 是否启用影视元数据 |
| `-dytt-base-url` | `HORIZON_DYTT_BASE_URL` | `https://dytt.org.cn` | 影视元数据来源 |
| `-dytt-cache-ttl` | `HORIZON_DYTT_CACHE_TTL` | `30m` | 元数据缓存时长 |
| `-dytt-min-interval` | `HORIZON_DYTT_MIN_INTERVAL` | `400ms` | 抓取最小间隔（礼貌限速）|
| `-tmdb-api-key` | `HORIZON_TMDB_API_KEY` | 空 | TMDb API Key，留空关闭增强 |
| `-translate` | `HORIZON_TRANSLATE_ENABLED` | `true` | 中文关键词自动翻译 |
| `-tpb-base` | `HORIZON_TPB_BASE` | `https://apibay.org` | 主种子索引源 |
| `-csv-base` | `HORIZON_CSV_BASE` | `https://torrents-csv.com` | 补充种子索引源 |
| `-csv` | `HORIZON_CSV_ENABLED` | `true` | 是否启用补充源 |

### API 概览

**公开接口**（无需登录，有限流）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/healthz` | 健康检查，返回版本号 |
| GET | `/api/v1/search?q=&limit=` | 种子检索（多源聚合 + 翻译）|
| GET | `/api/v1/top?limit=` | 热门种子 |
| GET | `/api/v1/dytt/list?category=` | 影视分类列表 |
| GET | `/api/v1/dytt/latest?limit=` | 影视最新更新 |
| GET | `/api/v1/dytt/search?q=` | 影视搜索 |
| GET | `/api/v1/dytt/detail?id=` | 影视详情 |
| GET | `/api/v1/dytt/cover?url=` | 封面代理（白名单域名）|

**认证接口**

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/api/v1/auth/register` | 注册，返回 access + refresh |
| POST | `/api/v1/auth/login` | 登录，返回 access + refresh |
| POST | `/api/v1/auth/refresh` | 用 refresh 换新令牌 |

**需登录**（`Authorization: Bearer <access token>`）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/v1/torrents` | 任务列表 |
| POST | `/api/v1/torrents` | 新建任务（传 magnet）|
| GET | `/api/v1/torrents/:infoHash` | 任务详情 |
| PATCH | `/api/v1/torrents/:infoHash` | 暂停 / 继续 |
| DELETE | `/api/v1/torrents/:infoHash` | 删除任务 |
| GET | `/api/v1/torrents/:infoHash/file` | 下载文件 |

**管理员接口**（`/api/v1/admin/*`）：封禁哈希 / 关键词、审计日志、运行指标。

### 数据存放位置

**客户端**（`%LOCALAPPDATA%\MagDownloader\`）

```
data\users.db          账号数据库
data\history.json      下载历史
data\cloud.cfg         云端服务器地址（留空/不存在 = 本地模式）
data\login.cfg         DPAPI 加密的记住密码
data\captcha.day       当日免验证码状态
logs\app.log           运行日志
logs\login.log         登录审计
logs\op.log            操作日志
logs\error.log         崩溃日志
aria2c.exe             解包出的下载引擎
```

**服务端**（`-dir` 与 `-db` 指定）

```
horizon.db             数据库（SQLite 模式）
downloads\             下载完成的文件
```

### 安全说明

已经做的：

- 密码用 **bcrypt**（服务端）/ **PBKDF2-SHA256 10 万次迭代**（客户端本地库）哈希，绝不明文存储
- JWT 校验 issuer、过期时间、签名算法，并带 **jti** 保证每次签发唯一
- 下载任务按 owner 隔离，越权访问一律 404（不区分「不存在」与「不属于你」，避免探测）
- SSRF 防护：连接时按**校验过的实际 IP** 直连，堵住 DNS rebinding；禁用环境变量代理
- 磁力链接严格校验：40 位 info hash 且拒绝截断
- 公开接口限流；生产环境只信任回环代理，避免 `X-Forwarded-For` 伪造绕过
- 登录失败锁定；审计日志

需要你自己注意的：

- **服务端必须配 HTTPS**。JWT 走明文 HTTP 时，网络中间人可以直接拿走令牌。
  建议用 Nginx / Caddy 加 TLS，或至少限制在可信内网。
- 默认配置下**第一个注册的用户是管理员**，公开部署前请立刻注册占位，或先关掉注册接口。
- `%LOCALAPPDATA%\MagDownloader\data\login.cfg` 是「记住密码」的密文，用 DPAPI 绑定当前
  Windows 账户。**同机同账户可以直接解出明文**，这是本地工具的合理取舍，但它不是
  防止本机他人访问的安全边界。
- 「当日免验证码」状态文件删掉即可重置，同样不是安全边界。

### 已知限制

- **客户端不预置任何服务器地址**，影视库与云端离线下载需要先在「服务器设置」里
  填写自己部署的 HorizonServer 地址。不填则这三项功能不可用（磁力搜索会回退到
  本地公开索引源），界面会明确提示原因，不会静默失败。
- **内容源 `dytt.org.cn` 存在不可用的情况**（Cloudflare 522）。服务端会优雅降级为
  503 + 明确提示，并回退过期缓存；磁力搜索与下载不受影响。生产使用建议准备备用源。
- 磁力解析依赖 DHT / Tracker 网络。在封堵 P2P 的网络（部分公司 WiFi、校园网）下
  会解析超时，这是网络限制而非程序问题。
- 客户端定位为 Windows 桌面工具，未做跨平台。
- Redis / MinIO / PostgreSQL 三种后端代码完整且有单元测试，但端到端验证需要真实服务。

### 技术栈

| | |
|---|---|
| 客户端 | C# 5（.NET Framework 4.8）、WinForms、System.Web.Extensions |
| 下载引擎 | aria2c 1.37（内嵌）|
| 服务端 | Go 1.26、Gin、golang-jwt/v5、bcrypt、modernc.org/sqlite |
| 可选依赖 | Redis、MinIO / S3、PostgreSQL、TMDb |

### 许可

[MIT License](LICENSE)

第三方内容归属：

- 下载引擎 [aria2](https://github.com/aria2/aria2) — GPLv2，未包含在本仓库中，请从其官方发布获取
- 种子索引数据来自 [apibay](https://apibay.org) / [torrents-csv](https://torrents-csv.com)
- 影视元数据来自第三方公开站点，版权归原权利人所有

---

## 📖 English Documentation

### What is this

A complete magnet download solution in two parts:

| Component | Stack | Role |
|---|---|---|
| **MagDownloader** | C# / .NET Framework 4.8 / WinForms | Desktop client: search, download, movie library, cloud task management |
| **HorizonServer** | Go 1.26 | Cloud service: BT offline download, multi-source torrent search, movie metadata proxy, auth |

They work independently or together. **The client ships with no server address baked in**:
out of the box it is a purely local downloader and magnet / BT / HTTP downloads work
immediately. Only after you enter a server address under "Server settings" do the three
server-backed features light up: the movie library, Chinese keyword search, and cloud
offline downloading.

### Features

**Client**

- **Bundled engine** — aria2c is embedded in the exe; double-click and run, no setup
- **Protocols** — magnet links, `.torrent` files, HTTP / HTTPS direct links
- **Multi-task** — live progress, speed, size and peer counts; pause, resume, delete
- **Torrent preview** — list the files inside a magnet (name + size) and download only what you pick
- **Video playback** — detects video files in a task and opens them in your default player
- **Fullscreen** — `F11` to toggle a borderless immersive layout
- **Torrent search** — queries multiple public indexers; Chinese keywords are translated first,
  then Chinese and English results are merged and de-duplicated
- **Movie library** — browse covers, titles and synopses; latest updates, categories, search, detail
- **Cloud download** — hand a magnet to the server, fetch the result back when done
- **Server settings** — configure the cloud address from the login screen or the status bar,
  with a built-in "Test connection" that echoes the server version
- **Accounts** — cloud-first auth with automatic local fallback; image captcha; lockout after 5 failures
- **Remember password** — encrypted at rest with Windows DPAPI, bound to the current Windows account
- **Skip captcha for the day** — after one successful login
- **Automatic token refresh** — expired access tokens are silently renewed and the call retried

**Server**

- **BT offline download** — full DHT / PEX / Tracker support, configurable port and uTP
- **Multi-source search** — concurrent apibay categories + torrents-csv variants + token
  matching + seeding-tier ranking
- **Auto translation** — Chinese queries are translated before searching; both original and
  Chinese titles are returned
- **Metadata proxy** — server-side fetching, in-memory cache and circuit-breaker fallback;
  the client never talks to the source site directly
- **Cover proxy** — host allowlist + SSRF guard + WebP→JPEG transcoding (old .NET cannot decode WebP)
- **Auth** — bcrypt password hashing + JWT (12h access / 30d refresh) + seamless key rotation
- **Login protection** — failure counting per account+IP, 10-minute lockout after 5 failures
- **Rate limiting** — separate buckets for public and cover endpoints; in-memory or Redis;
  automatic fallback if Redis fails
- **SSRF guard** — dials the validated IP directly, defeating DNS rebinding
- **Swappable storage** — SQLite (default, zero-dependency) or PostgreSQL
- **Optional extras** — Redis (distributed rate limiting), MinIO / S3 (auto-upload on completion)
- **Audit log** — logins, task changes and moderation actions are all recorded
- **Graceful shutdown** — waits for background work after `SIGTERM`

### Project layout

See the tree in the Chinese section above.

### Quick start

#### Option 1: just run the client

1. Download `MagDownloader.exe` from [Releases](../../releases)
2. Double-click it — no installation required
3. Click "注册" (Register) to create an account; the first account becomes the administrator

**Optional: connect to a cloud server**

Everything downloads fine without one. To use the movie library, Chinese keyword search or
cloud offline downloading:

1. Click "Server settings" at the bottom of the login screen (or "Cloud" in the status bar)
2. Enter the server address, e.g. `http://124.222.167.203:8080` or `https://your-domain.com`
3. Click "Test connection" to confirm connectivity and check the version, then "Save"

The address is stored in `data\cloud.cfg` and read back on the next launch. Saving it empty
returns the client to local-only mode.

#### Option 2: build the client from source

**Requirements**

- Windows 7 or later
- .NET Framework 4.8 (built into Windows 10/11)
- **aria2c.exe** — not included in this repository. Download the Windows 64-bit archive from
  [aria2 releases](https://github.com/aria2/aria2/releases) and extract `aria2c.exe`

**Build**

```powershell
cd MagDownloader

# Point at aria2c.exe (pick one)
$env:ARIA2_PATH = "D:\tools\aria2\aria2c.exe"   # 1: explicit file path
$env:ARIA2_DIR  = "D:\tools\aria2"              # 2: directory, searched recursively

# 3: or just run an older client once — it extracts to
#    %LOCALAPPDATA%\MagDownloader\aria2c.exe and the script finds it

powershell -ExecutionPolicy Bypass -File .\build.ps1
```

The script embeds both `aria2c.exe` and the icon, then verifies the resources are present.

#### Deploy the server

**Requirements**: Go 1.26 or later

```bash
cd HorizonServer

# Required: JWT signing secret (use a long random value in production)
export HORIZON_JWT_SECRET="$(openssl rand -hex 48)"

go build -o horizon-server .
./horizon-server -addr :8080 -dir ./downloads
```

The database and tables are created on first start. Verify:

```bash
curl -s http://127.0.0.1:8080/healthz
# {"service":"horizon-downloader","status":"ok","version":"1.3.0"}
```

**Docker**

```bash
cp .env.example .env      # replace every change-me value
docker compose up -d
```

`docker-compose.yml` wires up PostgreSQL, Redis, MinIO, Prometheus and Grafana.

**Cross-compile a Linux binary**

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -ldflags "-s -w -X horizon/internal/version.Version=1.3.0" \
  -o horizon-server-linux .
```

### Server configuration

Every option is available both as a flag and as an environment variable;
the environment variable wins.

| Flag | Environment variable | Default | Description |
|---|---|---|---|
| `-addr` | `HORIZON_ADDR` | `:8080` | HTTP listen address |
| `-dir` | `HORIZON_DIR` | `./downloads` | Where downloaded files are stored |
| `-jwt-secret` | `HORIZON_JWT_SECRET` | empty (**required**) | JWT signing secret |
| `-db-type` | `HORIZON_DB_TYPE` | `sqlite` | `sqlite` or `postgres` |
| `-db` | `HORIZON_DB` | `file:horizon.db?...` | SQLite DSN |
| `-postgres-dsn` | `HORIZON_POSTGRES_DSN` | empty | PostgreSQL DSN |
| `-listen-port` | `HORIZON_LISTEN_PORT` | `42069` | BT / DHT port (open both TCP and UDP) |
| `-dht` | `HORIZON_DHT_ENABLED` | `true` | Enable DHT |
| `-utp` | `HORIZON_UTP_ENABLED` | `false` | Enable uTP |
| `-redis-addr` | `HORIZON_REDIS_ADDR` | empty | Redis address; empty = in-memory limiter |
| `-redis-password` | `HORIZON_REDIS_PASSWORD` | empty | Redis password |
| `-s3-endpoint` | `HORIZON_S3_ENDPOINT` | empty | S3 / MinIO endpoint; empty = uploads off |
| `-s3-access-key` | `HORIZON_S3_ACCESS_KEY` | empty | S3 access key |
| `-s3-secret-key` | `HORIZON_S3_SECRET_KEY` | empty | S3 secret key |
| `-s3-bucket` | `HORIZON_S3_BUCKET` | `downloads` | S3 bucket |
| `-s3-use-ssl` | `HORIZON_S3_USE_SSL` | `false` | Use HTTPS for S3 |
| `-dytt` | `HORIZON_DYTT_ENABLED` | `true` | Enable movie metadata |
| `-dytt-base-url` | `HORIZON_DYTT_BASE_URL` | `https://dytt.org.cn` | Metadata source |
| `-dytt-cache-ttl` | `HORIZON_DYTT_CACHE_TTL` | `30m` | Metadata cache TTL |
| `-dytt-min-interval` | `HORIZON_DYTT_MIN_INTERVAL` | `400ms` | Minimum fetch interval (be polite) |
| `-tmdb-api-key` | `HORIZON_TMDB_API_KEY` | empty | TMDb API key; empty = enhancement off |
| `-translate` | `HORIZON_TRANSLATE_ENABLED` | `true` | Translate Chinese keywords |
| `-tpb-base` | `HORIZON_TPB_BASE` | `https://apibay.org` | Primary indexer |
| `-csv-base` | `HORIZON_CSV_BASE` | `https://torrents-csv.com` | Secondary indexer |
| `-csv` | `HORIZON_CSV_ENABLED` | `true` | Enable the secondary indexer |

### API overview

**Public** (no auth, rate limited)

| Method | Path | Description |
|---|---|---|
| GET | `/healthz` | Health check, returns the version |
| GET | `/api/v1/search?q=&limit=` | Torrent search (multi-source + translation) |
| GET | `/api/v1/top?limit=` | Popular torrents |
| GET | `/api/v1/dytt/list?category=` | Movie categories |
| GET | `/api/v1/dytt/latest?limit=` | Latest movies |
| GET | `/api/v1/dytt/search?q=` | Movie search |
| GET | `/api/v1/dytt/detail?id=` | Movie detail |
| GET | `/api/v1/dytt/cover?url=` | Cover proxy (allowlisted hosts only) |

**Auth**

| Method | Path | Description |
|---|---|---|
| POST | `/api/v1/auth/register` | Register; returns access + refresh |
| POST | `/api/v1/auth/login` | Login; returns access + refresh |
| POST | `/api/v1/auth/refresh` | Exchange refresh for a new pair |

**Authenticated** (`Authorization: Bearer <access token>`)

| Method | Path | Description |
|---|---|---|
| GET | `/api/v1/torrents` | List tasks |
| POST | `/api/v1/torrents` | Add a task (magnet) |
| GET | `/api/v1/torrents/:infoHash` | Task detail |
| PATCH | `/api/v1/torrents/:infoHash` | Pause / resume |
| DELETE | `/api/v1/torrents/:infoHash` | Delete task |
| GET | `/api/v1/torrents/:infoHash/file` | Download the file |

**Admin** (`/api/v1/admin/*`): blocked hashes / keywords, audit log, runtime metrics.

### Where data lives

**Client** (`%LOCALAPPDATA%\MagDownloader\`)

```
data\users.db          account database
data\history.json      download history
data\cloud.cfg         cloud server address (empty or absent = local-only mode)
data\login.cfg         DPAPI-encrypted remembered password
data\captcha.day       skip-captcha-for-today state
logs\app.log           application log
logs\login.log         login audit
logs\op.log            operation log
logs\error.log         crash log
aria2c.exe             extracted download engine
```

**Server** (paths given by `-dir` and `-db`)

```
horizon.db             database (SQLite mode)
downloads\             completed downloads
```

### Security notes

What is already handled:

- Passwords are hashed with **bcrypt** (server) / **PBKDF2-SHA256, 100k iterations** (client
  local database); nothing is stored in plain text
- JWT validation covers issuer, expiry and signing algorithm, plus a **jti** so every issued
  token is unique
- Download tasks are isolated by owner; cross-user access returns 404 (the same response for
  "does not exist" and "not yours", to avoid probing)
- SSRF guard dials the **validated IP** directly, defeating DNS rebinding; environment proxies
  are disabled
- Strict magnet validation: exactly 40 hex characters for the info hash, truncated hashes rejected
- Public endpoints are rate limited; in production only loopback proxies are trusted, so
  `X-Forwarded-For` cannot be forged to bypass IP limits
- Login lockout and audit logging

What you must handle yourself:

- **Put the server behind HTTPS.** With plain HTTP, anyone on the network path can steal a JWT.
  Use Nginx / Caddy for TLS, or at least keep it on a trusted network.
- **The first registered user becomes the administrator.** On a public deployment, register a
  placeholder immediately or disable registration first.
- `%LOCALAPPDATA%\MagDownloader\data\login.cfg` holds the remembered password encrypted with
  DPAPI and bound to the current Windows account. **The same user on the same machine can
  decrypt it.** That is a reasonable trade-off for a local tool, but it is not a boundary
  against someone else using your Windows session.
- The skip-captcha state file can be reset by deleting it; likewise not a security boundary.

### Known limitations

- **The client pre-configures no server address.** The movie library and cloud offline
  downloading require entering the address of your own HorizonServer under "Server settings".
  Without it those features stay unavailable (torrent search falls back to local public
  indexers) and the UI always states the reason instead of failing silently.
- **The metadata source `dytt.org.cn` is sometimes unreachable** (Cloudflare 522). The server
  degrades gracefully to a 503 with a clear message and falls back to stale cache; torrent
  search and downloading are unaffected. Have a backup source ready for production.
- Magnet resolution needs the DHT / Tracker network. On networks that block P2P (some corporate
  WiFi, campus networks) resolution will time out. That is a network restriction, not a bug.
- The client targets Windows desktop only; it is not cross-platform.
- The Redis / MinIO / PostgreSQL backends are complete and unit-tested, but end-to-end
  verification requires real services.

### Tech stack

| | |
|---|---|
| Client | C# 5 (.NET Framework 4.8), WinForms, System.Web.Extensions |
| Download engine | aria2c 1.37 (embedded) |
| Server | Go 1.26, Gin, golang-jwt/v5, bcrypt, modernc.org/sqlite |
| Optional | Redis, MinIO / S3, PostgreSQL, TMDb |

### License

[MIT License](LICENSE)

Third-party attribution:

- Download engine [aria2](https://github.com/aria2/aria2) — GPLv2. **Not** included in this
  repository; obtain it from its official releases.
- Torrent index data from [apibay](https://apibay.org) / [torrents-csv](https://torrents-csv.com)
- Movie metadata comes from third-party public sites; copyright remains with the respective owners
