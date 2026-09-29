# 地平线磁力下载 · 云端服务（Horizon Server）

按技术路线将「地平线磁力下载」升级为**云端下载服务**的服务端。本仓库实现三个阶段的完整服务端：Go 下载引擎 + Gin 后端 + REST/JWT/WebSocket + SQLite/PostgreSQL 存储 + Redis 限流 + 离线下载 S3 导出 + 安全合规黑名单 + 监控 + Docker/K8s/CI-CD 部署包。


### 运维：日志上限

访问日志经 systemd 收进 journal，默认只受磁盘空间限制。生产机建议安装
`deploy/journald-horizon.conf`：

```bash
sudo cp deploy/journald-horizon.conf /etc/systemd/journald.conf.d/horizon.conf
sudo systemctl restart systemd-journald
```

上限 500M / 单文件 100M / 保留 4 周，避免长期运行把磁盘写满。

### 运维：版本与更新检查

`/healthz` 会返回服务端版本：

```bash
curl -s http://127.0.0.1:8080/healthz
# {"service":"horizon-downloader","status":"ok","version":"1.3.0"}
```

编译时注入版本：`go build -ldflags "-X horizon/internal/version.Version=1.3.0"`。
客户端「云端下载」页底部会同时显示「客户端 vX · 服务端 vY」，
据此可以判断两端是否配套，不必比对文件时间。


## 技术栈对照

| 技术路线要求 | 实现 | 说明 |
|---|---|---|
| 下载引擎（Go + anacrolix/torrent） | ✅ `internal/engine` | DHT/PEX/uTP/协议加密默认开启 |
| 后端 API（Go Gin） | ✅ `internal/api` | REST + 健康检查 + `/metrics` |
| 用户认证（JWT） | ✅ `internal/auth` | bcrypt 密码 + 15 分钟 access token |
| 实时推送（WebSocket） | ✅ `/api/v1/ws/progress` | 每秒推送，避免轮询 |
| 关系数据库（PostgreSQL 16+） | ✅ `internal/store` | `Backend` 接口 + SQLite（默认）/ PostgreSQL（pgx）双实现，`-db-type` 切换 |
| Redis（限流 / 会话） | ✅ `internal/ratelimit` | 登录/注册限流：Redis（多实例）或内存（单机） |
| 安全合规（SSRF / 黑名单 / 审计） | ✅ `internal/guard` + store | info_hash/关键词黑名单、DMCA 审计、扫描移除 |
| 离线下载 + S3 | ✅ `internal/cloud` | 完成后打包上传 S3/MinIO + 预签名链接（含单元测试） |
| 监控（Prometheus/Grafana/告警） | ✅ | `/metrics` + `prometheus/` 告警规则 + compose 服务 |
| K8s / CI-CD | ✅ | `k8s/horizon.yaml` + `.github/workflows/ci.yml` |
| 影视元数据（电影天堂） | ✅ `internal/dytt`（+ 可选 `internal/tmdb`） | 封面/名称/简介抓取 + WebP 转码 + 封面代理；供客户端影视库使用 |

## 快速开始（本机）

```bash
cd HorizonServer
go build -o horizon-server .
./horizon-server -jwt-secret <随机字符串> -addr :8080        # 默认 SQLite
./horizon-server -db-type postgres -postgres-dsn 'postgres://...'   # 切 PostgreSQL
# Windows 下为 horizon-server.exe
```

## 存储 / 缓存 / 对象存储切换

- **数据库**：`-db-type sqlite`（默认，零依赖）或 `-db-type postgres -postgres-dsn <DSN>`。
- **限流**：`-redis-addr <host:port>` 启 Redis 限流（留空用内存限流）。
- **S3 离线导出**：`-s3-endpoint <host:port> -s3-access-key <k> -s3-secret-key <s> -s3-bucket <bucket>` 启用；任务完成后后台 worker 自动打包上传并记录预签名链接。

## 影视元数据（封面 / 名称 / 简介）

供桌面端「影视库」使用的影视元数据接口。**抓取一律在服务端完成**：内容源（dytt.org.cn）
对同一 IP 的高频请求会直接返回 HTTP 400，客户端直连必被限流，所以由服务端统一做
限速（默认 400ms/次）、指数退避重试与内存缓存，客户端只消费下面的接口。

> 说明：`home.dytiantang.com.cn` 本身只是一个用全屏 iframe 包住 `dytt.org.cn` 的外壳页，
> 首页卡片是写死的占位数据；真实内容源是 `dytt.org.cn`，服务端直接对接内容源。

```
GET /api/v1/dytt/latest?limit=20          # 首页「每日更新」
GET /api/v1/dytt/list?category=movie&page=1   # 分类片单（movie/tv/short 或 3/2/21）
GET /api/v1/dytt/search?q=关键词            # 按片名搜索
GET /api/v1/dytt/detail?id=266003         # 详情：封面/名称/简介/演员/线路/检索关键词
GET /api/v1/dytt/cover?url=<封面直链>       # 封面代理（仅放行白名单图床，防 SSRF）
```

- 全部为**公开接口，无需登录**（片名、简介、封面都属于公开元数据）。
- 封面代理会**把 WebP 转成 JPEG**：图床大量用 WebP，且常把 WebP 内容挂在 `.jpg`
  地址上、`Content-Type` 还谎报成 `image/jpeg`；.NET Framework 的
  `System.Drawing.Image` 解不了 WebP，所以在服务端转掉，客户端才能稳定显示。
- 源站**只提供在线播放，不含磁力链接**，因此详情返回的是 `magnet_queries`
  （精确检索关键词）而不是伪造的磁力地址；客户端可用它去「搜索」里找资源。
- 环境变量：`HORIZON_DYTT_ENABLED`（默认 true，设 false 可完全关闭外呼）、
  `HORIZON_DYTT_BASE_URL`、`HORIZON_DYTT_CACHE_TTL`、`HORIZON_DYTT_MIN_INTERVAL`、
  `HORIZON_DYTT_TIMEOUT`（单次请求，默认 5s）、
  `HORIZON_DYTT_CALL_TIMEOUT`（一次抓取含重试的总时限，默认 12s）。
- 可选 TMDb 增强：配 `HORIZON_TMDB_API_KEY` 后，`detail?enhance=1` 会额外补充
  TMDb 的简介/大图封面/评分；不配则完全跳过。

### 内容源故障时的行为（重要）

内容源是**外部第三方站点**，会挂。2026-09-29 实测 `dytt.org.cn` 在 Cloudflare 后面
返回 522、部分时段直接连接超时，全部路径（含 RSS 与详情页）都拿不到数据。

**注意 host 是本机自测时的现象，不代表线上**：本机 curl/浏览器访问该站同样超时，
问题在源站而非本项目。

原始实现在这种场景下有两个真实缺陷，都已修掉：

| 缺陷 | 后果 | 现在 |
|---|---|---|
| 单次 15s × 重试 3 次 + 退避 ≈ **46.5s** | 客户端读超时只有 20s，用户只看到「无法连接云端服务…操作超时」，真正原因一句都传不回去 | 5s × 2 + 退避 ≈ 10.5s，服务端先于客户端返回 |
| 源站故障期间每个请求都去撞一次上游 | 每次都白等一个完整超时 | 熔断 60s：冷却期内**直接返回**（实测 0.0008s），源站恢复后第一次成功即自动解除 |
| 有缓存也照样报错 | 用户什么都看不到 | **回退过期缓存**，返回 200 + `stale: true` + `hint` |

错误分级也随之明确（`writeDyttError`）：

- 源站连不上 → **503** + `{"error":"内容源（电影天堂）暂时不可用，请稍后重试。这不影响磁力搜索与下载。","upstream":true,"retryable":true}`；
  客户端会自动带上「加载最新更新失败：」前缀并把这句话完整显示给用户。
- 被限流 → 429（源站是活的，只是要求降频，**不熔断**）。
- 404 → 404（资源不存在，**不重试、不熔断**）。

`stale` 标记不能省：回退的是旧快照，客户端要能如实告诉用户「这是上次抓取的片单」，
而不是让人以为站点不更新了。客户端会把 `hint` 直接显示在底部状态栏。

熔断不依赖缓存开关：就算运维把 `HORIZON_DYTT_CACHE_TTL=0` 关掉缓存，也不会让
每个请求去撞一个已知挂掉的源站。

## 种子检索（中文片名自动翻译 + 相关性过滤）

`GET /api/v1/search` 是客户端「搜索」页的检索通道，公开无需登录：

```
GET /api/v1/search?q=流浪地球&cat=207&limit=40
# -> {"results":[{"name":"The.Wandering.Earth.2019....",
#                "name_zh":"流浪地球  2019 DUBBED 1080p WEBRip DD5 x264 GalaxyRG", ...}],
#     "query":"流浪地球","search_term":"The Wandering Earth",
#     "translated":true,"translated_ok":true,"filtered_out":0,"name_zh_count":40,
#     "sources":["csv","tpb:207","tpb:201"],
#     "hint":"","count":67,"limit":120}
# sources / source_errors 可选，用于说明「结果为什么变少」；
# 每条结果里的 added 是发布时间（Unix 秒）。
```

索引站（apibay / The Pirate Bay）的库**几乎只含英文片名**，这是中文搜索失效的根因：

- 直接拿中文词检索，站点会当成无效词，返回满屏无关的欧美热门种子
  （实测搜「云雀叫天录」返回《蜘蛛侠：Brand New Day》《Project Hail Mary》；
  搜「流浪地球」「三体」「甄嬛传」各 100 条，**含关键词的 0 条**）。
- 换成英文片名检索则完全正常（「The Wandering Earth」40 条、「Ne Zha」39 条）。

因此 `internal/torsearch` 做了三件事：

1. **翻译**：关键词含中日韩字符时，先调腾讯 Transmart 翻成英文片名（译文按
   `HORIZON_TRANSLATE_TTL` 缓存，默认 24h；纯英文查询不触发翻译，零额外开销）。
   译文会清洗掉 `(电影名)` 这类括号注释。
2. **分类归并**：apibay 的 `cat=0`（全部）对多词查询不可靠（实测返回 0 条），
   因此 `cat` 留空或为 0 时会展开成影视分类分别查询再合并。
3. **相关性过滤**：按片名词元过滤结果，并剔除画质噪音词，`filtered_out`
   返回被过滤条数；译名质量差导致 0 条时，`hint` 会给出提示文案。

对比实测（同一网络、同一时刻）：

| 关键词 | 检索词 | 结果 |
|---|---|---|
| 流浪地球 | The Wandering Earth | 40 条（全相关） |
| 无间道 | Infernal Affairs | 34 条 |
| 云雀叫天录 | Skylark calls the sky record | 0 条 + hint |
| avatar | avatar（不翻译） | 100 条 |

> 冷门国产剧/短剧本就没有英文资源，翻译再准也搜不到——这是索引源的语言结构问题，
> 不是翻译质量问题。此时接口返回 `hint` 明确告知，避免客户端展示误导性结果。

环境变量：`HORIZON_TRANSLATE_ENABLED`（默认 true）、`HORIZON_TRANSLATE_URL`、
`HORIZON_TRANSLATE_TTL`（默认 24h）、`HORIZON_TPB_BASE`。
多索引源：`HORIZON_CSV_BASE`（默认 `https://torrents-csv.com`）、
`HORIZON_CSV_ENABLED`（默认 true，设 false 只走 apibay）。
返回结果中的 `name_zh`（英文片名回译中文）见下一节。

### 反向：结果里的英文种子名翻译回中文（`name_zh`）

搜索返回的种子名是英文的（`The.Wandering.Earth.2019.DUBBED.1080p.WEBRip.1400MB.DD5.1.x264-GalaxyRG`），
用户读不出片名。接口为每条结果额外返回 `name_zh`。

**绝不能整串翻译。** 把整串丢给翻译接口会把画质、封装、组名一起翻烂，得到
「流浪地球2019配音1080p」这类没法看的东西。做法是：

1. `splitSeedName` 先切出**片名**，切分顺序为「季集标记 `SxxExx` → 年份 → 画质词」。
   季集比年份可靠所以优先：`Futurama S14E10` 里 `14` 不是年份。
2. 只把片名送去翻译（`TranslateBatch`，一批一次请求，实测 40 条约 1~2 秒）。
3. `joinNameZh` 把中文片名与**原样保留**的技术标签拼回去：

```
The.Wandering.Earth.2019.DUBBED.1080p.WEBRip.1400MB.DD5.1.x264-GalaxyRG
  -> name_zh: "流浪地球  2019 DUBBED 1080p WEBRip DD5 x264 GalaxyRG"
```

剧集保留 `S01E07` 标记。以下情况 `name_zh` 为空串（表示「这条不显示中文」）：
译文为空、译文不含中文（`Futurama` → `Futurama`）、译文与原名相同。

**英文原名不会被覆盖。** 翻译会错（实测 `Avatar.2009.EXTENDED...` 译成「化身」，
应为「阿凡达」），英文原名才是判断资源真假的依据，客户端两列并排展示。

两个缓存分开：`cache`（中→英，检索用）、`cacheRev`（英→中，展示用），互不覆盖。
写入时惰性清理过期条目，避免长期运行内存无界增长。

### 耗时预算（`SearchBudget`）

搜索耗时是**串行叠加**的：翻译关键词 + 逐个分类检索 + 翻译结果片名。
不加约束的最坏情况是「翻译 12s + 3 个分类 × 15s」≈ 57 秒，远超客户端等待上限——
用户看到的不再是「慢」，而是「搜索失败」（客户端超时后丢弃全部结果）。

`Config.SearchBudget`（默认 20s）给整条链路一个总预算：

- **分类检索只拿 60%**（12s）。上游一拖，后面的翻译就会被挤没，用户只能看到一堆英文名。
- 预算耗尽时**停止后续分类**，已拿到的结果仍然有效。
- 预算耗尽是「我们主动放弃」，返回空结果 + `hint`，而不是报错。只有上游真的故障才报错。

对应地，客户端的等待上限（`CloudTimeoutMs = 30000`）必须**大于**服务端预算，
否则服务端还在认真干活、客户端已经放弃，那部分工作纯属白做。

### 返回条数（`limit`）

`GET /api/v1/search?q=...&limit=40`。limit 不只是省流量：**每多返回一条，就多一条片名要翻译**。
实测搜「avatar」返回 200 条要 8~10 秒，返回 40 条只要 1.8 秒，而用户根本翻不到第 200 条。
客户端固定请求 120 条。


### 多索引源：apibay + torrents-csv

单一索引源是“搜不全”的根因：apibay（The Pirate Bay）只收英文片名，
而且长尾新片偏少。`internal/torsearch` 现在**并发**打两条索引路，
再合并去重：

| 源 | 地址 | 特点 |
|---|---|---|
| `tpb` | `apibay.org`（`HORIZON_TPB_BASE`） | 英文片名为主，分类齐全，可翻页 |
| `csv` | `torrents-csv.com`（`HORIZON_CSV_BASE`） | 库更杂、更新更快，**收纯中文片名种子** |
| `csv:zh` | 同上，但用用户原始中文词检索 | 仅在发生中→英翻译时多发一次 |

关键行为：

- **中文原词也要查一遍 csv**。csv 里有 `[DBD-Raws][流浪地球][1080P]`
  这种纯中文名种子，用翻译后的英文词一条都查不到。
- **并发而非串行**：两条路各自约 0.8s，串起来就是 1.6s；且任何一条挂了
  都不能拖住另一条。
- **一条路完全失败不影响另一条**。只有“所有源都没跑通且带错误”
  才返回错误；“跑通但没结果”不算失败。
- **infohash 统一大写**后去重，否则同一种子会在两源各立一条。
- `Result.Sources` / `Result.SourceErrors` 告诉客户端“哪些源真正出了结果、
  哪些源试过但失败”，让“结果变少了”可解释。

实测（生产服务器，`limit=0` 不截断）：

| 关键词 | 原单源 | 新双源 | 近一年发布 |
|---|---|---|---|
| 流浪地球 | 40 | **67** | 4 |
| Interstellar | 100 | **121** | 17 |
| 无间道 | 34 | **49** | 4 |
| 疯狂动物城2 | 0 | **1** | 1 |

上限说明：torrents-csv 的 `?size=` 实测最多只回 25 条且**不能翻页**
（`size=1000`、`offset`、`page` 均无效）。
曾经想用「换排序各取一遍」来多拿，但实测证明该站的 `order_by` **被忽略**：
四种排序在同一关键词下返回内容的 md5 完全相同，那两次请求是纯浪费。
现在只发一次，改用「多给几个检索词」扩大覆盖（`csvVariants`）：
整串满了 25 条才补查「去首词的整串」，绝不补查单个词。

### 「全部」下同时打分类与 cat=0

apibay 的 `cat=0`（全站）与「逐个影视分类」覆盖的**不是**同一批种子。
2026-09-29 复测：

| 检索词 | 分类并集 | cat=0 | 分类独有 | cat=0 独有 |
|---|---|---|---|---|
| Infernal Affairs | 40 | 79 | 12 | 51 |
| Dune Part Two | **0** | **97** | 0 | 97 |

所以「全部」下两者都要打：只打分类会漏一大片（Dune 那例直接是 0 条），
只打 cat=0 也会漏。用户显式选了某个分类时不做这一步——那时要的就是那个分类。

> 早前 README 里「cat=0 对多词查询返回 0 条」的结论已被推翻：那是上游抖动，
> 不是参数语义问题。不要再据此把 cat=0 排除掉。

### 成本：为什么不能只靠「多查几个词」换召回

扩宽覆盖最省事的做法是把关键词拆细多查几遍，但这会直接毁掉相关性。
实测教训：为「流浪地球」（译作 "The Wandering Earth"）补查了单词
"Earth"，在「命中任一词元即相关」的过滤下，111 条结果里混进 **57 条无关**的
（Earth 3D Suite、Orb: On the Movements of the Earth、Alien Earth…）。

因此定了两条硬规矩：

1. `relevant()` 要求**命中全部词元**，不是任一。
2. `csvVariants` **只产出整串类变形**（如去首词的整串），绝不补查单个词。

代价要认：查「Dune Part Two」不再返回 2021 年的《Dune》。
这是刻意取舍——用户搜什么片就给什么片，比多给两倍近义词更有用。

改完后生产实测（`limit=0`，以下为命中检索词的条数）：

| 关键词 | 修前 | 修后 | 可疑条数 |
|---|---|---|---|
| 流浪地球 | 111（其中 57 条无关） | **86** | 1 |
| Interstellar | 204 | **204** | 0 |
| 无间道 | 94 | **94** | 0 |
| Dune Part Two | 130 | **107** | 0 |
| 疯狂动物城 | 161 | **161** | 0 |

### 排序：热度分档 + 同档看新旧

纯按做种数排会让“三年前的老片源”永远压着“上个月的新片源”——
做种数天然偏向旧种子（积累时间长）。现在先按 `seedTier()` 分档（做种数差一个
数量级才算“更热”），同档内按发布时间降序。

`Item.Added`（Unix 秒）统一承载两源的时间字段：apibay 返回 `added`，
torrents-csv 返回 `created_unix`，`0` 表示上游没给。

## BT 网络：Tracker 分层与 DHT 开关

`internal/engine` 的 tracker 列表改为**分层**结构，HTTP/HTTPS 优先、UDP 其次：

- 部分网络会**针对性封锁 BT 的 UDP 流量**（实测全部 UDP tracker 与 DHT 引导节点
  无回包，而同一台机器 UDP DNS 到 114.114.114.114 正常回包 44 字节）。
  分层后先用 HTTP tracker 拿到 peer，避免把时间浪费在被丢包的 UDP 上。
- 实测唯一确认有效的 HTTP tracker 是 `https://tracker.foreverpirates.co:443/announce`
  （返回合法 bencode）；`tracker.zhuqiy.com` 返回 HTTP 400，
  多处 tracker 域名被 DNS 污染（`getaddrinfo failed`）。
- 新增 `HORIZON_DHT_ENABLED`（默认 true）：在被封锁 BT/UDP 的网络里设为 false，
  可避免 DHT 无谓等待，仅靠 tracker 与 PEX 找 peer。

### 两个真实 bug（已修复，实测验证）

排查「连上 peer 却 0 字节」时定位到两个独立缺陷，都不是网络问题：

1. **`AddMagnet` 从未声明要下载**（`internal/engine/engine.go`）。anacrolix 的
   `AddMagnet` 只创建 torrent 并解析元数据，新 torrent 的 piece 优先级初始是
   `None`；不显式调用 `DownloadAll()` 就永远不请求任何数据。
   注意 `DownloadAll()` 内部读 `t.info.NumPieces()`，元数据未到时调用会**空指针
   panic**（上游注释明确要求先等 `GotInfo`），因此在 `<-t.GotInfo()` 之后异步调用。
2. **uTP 未关闭**。uTP 走 UDP，而 UDP 被封锁时握手能走 TCP、数据阶段切到 uTP 即被
   丢包。实测同一 magnet：开 uTP 仅 3 连接/0 种子，关 uTP 7 连接/6 种子。

修复后服务器实测（Ubuntu 24.04 ISO，51 做种）：

| 时间 | peers | 速度 | 已下载 |
|---|---|---|---|
| 30s | 20 | — | 9.9 MB |
| 90s | 33 | 0.8 MB/s | 56.5 MB |
| 180s | 49 | **8.0 MB/s** | 472.4 MB |

> 本机网络下**低做种的影片类磁力**依旧拿不到元数据（BT 的 UDP 与 tracker 域名
> 被针对性封锁），「云端下载」是这类网络里唯一可靠出路。

## API 概览

```
GET    /healthz                          # 健康检查
GET    /metrics                          # Prometheus 指标
GET    /api/v1/search?q=流浪地球&cat=207  # 种子检索（公开；中文片名自动翻译）
GET    /api/v1/top                          # 热门种子（公开）
POST   /api/v1/auth/register             # 注册 -> token（首个用户=管理员；有限流）
POST   /api/v1/auth/login                # 登录 -> token（有限流）
POST   /api/v1/torrents                  # 添加磁力链接（需 Bearer；命中黑名单→403）
GET    /api/v1/torrents                  # 任务列表
GET    /api/v1/torrents/:infoHash        # 单任务
PATCH  /api/v1/torrents/:infoHash        # {"action":"pause"|"resume"}
DELETE /api/v1/torrents/:infoHash        # 删除
WS     /api/v1/ws/progress               # 进度推送（1s/次）

# 管理员接口（需 Bearer 且 is_admin=true）
POST   /api/v1/admin/blocked-hashes      # {"info_hash":"40位hex","reason":""}
GET    /api/v1/admin/blocked-hashes      # 列出被禁哈希
DELETE /api/v1/admin/blocked-hashes/:infoHash
POST   /api/v1/admin/blocked-keywords    # {"keyword":"..."}
GET    /api/v1/admin/blocked-keywords
DELETE /api/v1/admin/blocked-keywords?keyword=...
GET    /api/v1/admin/audit               # DMCA/拦截审计日志
POST   /api/v1/admin/enforce-blacklist   # 扫描并移除命中黑名单的现有任务
```

### 影视元数据接口（公开，无需登录）

```
GET    /api/v1/dytt/latest?limit=20            # 首页每日更新
GET    /api/v1/dytt/list?category=movie&page=1 # 分类片单（movie/tv/short）
GET    /api/v1/dytt/search?q=火种               # 按片名搜索
GET    /api/v1/dytt/detail?id=266003           # 详情（封面/名称/简介/检索关键词）
GET    /api/v1/dytt/cover?url=<封面直链>        # 封面代理（WebP 自动转 JPEG）
```

## 安全合规

- **SSRF 防护**（`internal/guard`，含单元测试）：拒绝回环/内网/链路本地/云元数据地址，域名解析后逐 IP 校验（防 DNS rebinding）。
- **信息哈希黑名单**：`add_torrent` 创建任务前校验，命中返回 403；管理员可增删。
- **关键词黑名单**：对磁力链接（及任务名）做不区分大小写子串匹配，命中返回 403。
- **DMCA 审计日志**：拦截、封禁增删、扫描移除等动作写入 `audit_log`。
- **扫描移除**：`enforce-blacklist` 扫描现有任务并移除命中者（可挂定时器周期性调用）。
- 由 `TestSecurityCompliance`（httptest 集成测试）+ `TestRateLimit` + `guard`/`cloud`/`ratelimit` 单元测试覆盖。

## 监控

`/metrics` 输出 Prometheus 指标（活跃/完成/总任务数、用户数）。`prometheus/rules.yml` 内置 `HorizonDown` / 活跃任务过多 / 用户数异常三条告警规则。compose 已带 Prometheus（9090）+ Grafana（3000）。

## Docker 部署（一键全栈）

```bash
cd HorizonServer
cp .env.example .env      # 编辑，改强随机密钥
docker compose up -d --build
# app:8080, prometheus:9090, grafana:3000, minio 控制台:9001
# 默认 PostgreSQL + Redis + MinIO；设 DB_TYPE=sqlite 可回退单机
```

## 部署到 124.222.167.203

服务器 SSH(22) 已确认可达。假设有 ssh/root 权限：

```bash
# 1) 打包源码上传
scp -r HorizonServer root@124.222.167.203:/opt/horizon

# 2) 服务器上已装 Docker 则：
ssh root@124.222.167.203 "cd /opt/horizon && \
  echo HORIZON_JWT_SECRET=$(openssl rand -hex 32) > .env && \
  echo POSTGRES_PASSWORD=$(openssl rand -hex 16) >> .env && \
  echo MINIO_ROOT_PASSWORD=$(openssl rand -hex 16) >> .env && \
  docker compose up -d --build"

# 3) 验证（在任意可联网机器）：
curl http://124.222.167.203:8080/healthz
```

> 注意：80 端口当前未开通；Docker 映射的是 8080。需在服务器防火墙/安全组放行 8080。

## 当前局限（诚实说明）

- **BT 下载在本机网络基本不可用（已定量实测）**：本机对 BT 的 UDP 流量做了针对性封锁
  （全部 UDP tracker 与 DHT 引导节点无回包，而 UDP DNS 正常），且大量 tracker 域名被
  DNS 污染。实测 `.torrent` 文件 + 51 做种能跑起来，但**低做种的影片类磁力 600 秒
  0 字节**，元数据都拿不到。这是网络限制而非代码错误；tracker 已改为 HTTP 优先分层，
  并可用 `HORIZON_DHT_ENABLED=false` 关掉 DHT 的无谓等待。**云端下载是这类网络下
  唯一可靠出路**，需在放行 BT 的服务器上部署本服务端。
- **中文磁力源未能新增**：实测 Bitsearch / BTDig / Nyaa / 1337x / TorrentGalaxy /
  LimeTorrents / TorrentKitty / MagnetDL / sukebei / searx.be 全部超时（Knaben 503，
  BTSOW/btsearch 返回 200 但 0 条磁力）。因此走的是「翻译中文片名 → 检索英文索引站」
  这条路，而非新增中文源。中文索引源在可访问的网络下可通过 `HORIZON_TPB_BASE`
  换成兼容 apibay API 的站点。
- **TMDb 不可达时自动降级**：`api.themoviedb.org` / `image.tmdb.org` 在本机不可达，
  此时 `dytt/detail` 的 `english_title` 回退用翻译接口生成（已实测）。
- **PostgreSQL / Redis / S3 需部署环境验证**：三者的客户端代码均编译通过并有单元测试，但端到端运行需连接真实服务（本机 Docker 引擎未启动，无法本地起量化验证）；`docker compose` 已完整编排，部署到 124.222.167.203 后即可验证。
- **任务重启不自动续传**：任务元数据已持久化（SQLite/PostgreSQL），但进程重启后活跃传输不自动恢复（待接入断点续传队列）。
- **仅支持磁力链接**：`magnet:?` 开头；`.torrent` URL 抓取及配套 SSRF 校验已备好（`internal/guard`），待后续阶段启用。

## 目录结构

```
HorizonServer/
├── main.go                  # 入口：装配（DB/Redis/S3）+ 优雅关闭 + S3 worker
├── internal/
│   ├── auth/                # bcrypt + JWT + 中间件
│   ├── store/               # Backend 接口 + SQLite/PostgreSQL 双实现
│   ├── ratelimit/           # 内存 + Redis 限流
│   ├── cloud/               # S3/MinIO 上传 + 预签名（含测试）
│   ├── engine/              # anacrolix/torrent 下载引擎
│   ├── api/                 # REST 路由 + WebSocket + 集成测试（含 dytt 接口与测试）
│   ├── dytt/                # 电影天堂元数据抓取：限速/重试/解析/封面代理（含测试与样本）
│   ├── torsearch/           # 种子检索：中文片名翻译 + 分类归并 + 相关性过滤 + 片名回译中文（含测试）
│   ├── tmdb/                # TMDb 增强（可选，未配 Key 时自动跳过）
│   └── guard/               # SSRF 防护 + info_hash 提取（含测试）
├── k8s/horizon.yaml         # Kubernetes 部署清单
├── .github/workflows/ci.yml # CI-CD
├── prometheus/              # 告警规则
├── Dockerfile
├── docker-compose.yml       # 全栈编排（app+postgres+redis+minio+prometheus+grafana）
├── prometheus.yml
└── .env.example
```

## 客户端对接（已完成）

桌面端「地平线磁力下载」（`../MagDownloader`）已接入云端登录：

- 新增 `CloudClient.cs`：`CloudConfig`（服务器地址，**默认不预置**，由客户端「服务器设置」写入 `data\cloud.cfg`）+ `CloudAuth`（调 `/api/v1/auth/register` 与 `/login`）。
- 登录/注册**云端优先**：服务器可达则以云端为准；仅当**服务器不可达**（网络错误）时才回退本地账密，保证离线仍可用。
- 首个云端注册用户自动成为**管理员**（与本地版首次注册一致）。
- 服务端地址可用 `CloudConfig.Save(url)` 写入本地 `cloud.cfg`。

影视库（电影天堂元数据）也已接入：

- 新增 `MovieMeta.cs`：`DyttApi`（调 `/api/v1/dytt/*`）+ `CoverCache`（封面磁盘/内存缓存，按需缩放）。
- 新增 `MovieBrowse.cs`：海报墙（自绘，封面异步加载）+ 详情面板（封面/简介/主演/线路/检索关键词）。
- 主界面左侧新增「影视库」导航项；详情里点「搜索磁力」会把关键词带到种子搜索页自动检索。
- 封面统一走服务端代理并缓存到 `%LOCALAPPDATA%\MagDownloader\cache\covers`。

检索通道也已接入云端：

- `TorrentSearch.cs` 的 `Search()` 改为**云端优先、本地兜底**：先打
  `/api/v1/search`，失败（服务端旧版 404 或不可达）才回退客户端直连 apibay。
- 云端返回的「实际检索词」（`search_term`）与提示（`hint`）会显示在搜索框下方，
  中文关键词能直观看到被翻译成了什么。
- 影视库详情点「搜索磁力」时只传**纯片名**（英文名优先），不再带 `1080p 磁力`
  这类后缀——实测长串中文被逐字翻译成 `Skylark calls the sky record 2026 1080p
  magnet` 只会搜出 0 条。

> 升级提示：影视库**与中文搜索**都依赖新版服务端。若服务端仍是旧版本，
> 客户端会提示「云端服务缺少影视元数据接口，请先升级服务器再试」，
> 搜索则自动回退本地直连（中文关键词命中率低）。部署新二进制后即可使用。

> 迁移说明：原有本地账号不会被自动搬入云端。部署云端后，请用原账号名在云端**重新注册一次**（首个注册者即管理员），后续即走云端鉴权。