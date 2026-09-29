package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	_ "modernc.org/sqlite"

	"horizon/internal/api"
	"horizon/internal/auth"
	"horizon/internal/cloud"
	"horizon/internal/dytt"
	"horizon/internal/engine"
	"horizon/internal/ratelimit"
	"horizon/internal/store"
	"horizon/internal/tmdb"
	"horizon/internal/torsearch"
)

func main() {
	addr := flag.String("addr", envOr("HORIZON_ADDR", ":8080"), "HTTP 监听地址")
	dbType := flag.String("db-type", envOr("HORIZON_DB_TYPE", "sqlite"), "数据库类型：sqlite | postgres")
	dsn := flag.String("db", envOr("HORIZON_DB", "file:horizon.db?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"), "SQLite DSN（db-type=sqlite）")
	pgDSN := flag.String("postgres-dsn", envOr("HORIZON_POSTGRES_DSN", ""), "PostgreSQL DSN（db-type=postgres）")
	jwtSecret := flag.String("jwt-secret", envOr("HORIZON_JWT_SECRET", ""), "JWT 签名密钥")
	maxConns := flag.Int("max-conns", 80, "单任务最大 Peer 连接数")
	dir := flag.String("dir", envOr("HORIZON_DIR", "./downloads"), "下载输出目录")
	listenPort := flag.Int("listen-port", envInt("HORIZON_LISTEN_PORT", 42069), "BT/DHT 监听端口（需在防火墙/安全组开放 TCP+UDP）")
	redisAddr := flag.String("redis-addr", envOr("HORIZON_REDIS_ADDR", ""), "Redis 地址（留空用内存限流）")
	redisPwd := flag.String("redis-password", envOr("HORIZON_REDIS_PASSWORD", ""), "Redis 密码")
	s3Endpoint := flag.String("s3-endpoint", envOr("HORIZON_S3_ENDPOINT", ""), "S3/MinIO 地址 ip:port（留空禁用离线导出）")
	s3Access := flag.String("s3-access-key", envOr("HORIZON_S3_ACCESS_KEY", ""), "S3 Access Key")
	s3Secret := flag.String("s3-secret-key", envOr("HORIZON_S3_SECRET_KEY", ""), "S3 Secret Key")
	s3Bucket := flag.String("s3-bucket", envOr("HORIZON_S3_BUCKET", "downloads"), "S3 桶名")
	s3SSL := flag.Bool("s3-use-ssl", envOr("HORIZON_S3_USE_SSL", "false") == "true", "S3 是否使用 HTTPS")
	dyttOn := flag.Bool("dytt", envOr("HORIZON_DYTT_ENABLED", "true") == "true", "启用电影天堂元数据（封面/名称/简介）")
	dyttBase := flag.String("dytt-base-url", envOr("HORIZON_DYTT_BASE_URL", "https://dytt.org.cn"), "影视元数据内容源地址")
	dyttRef := flag.String("dytt-referer", envOr("HORIZON_DYTT_REFERER", "https://home.dytiantang.com.cn/"), "抓取时携带的 Referer")
	dyttTTL := flag.Duration("dytt-cache-ttl", envDuration("HORIZON_DYTT_CACHE_TTL", 30*time.Minute), "元数据页内存缓存时长（0 表示关闭）")
	dyttInterval := flag.Duration("dytt-min-interval", envDuration("HORIZON_DYTT_MIN_INTERVAL", 400*time.Millisecond), "对内容源的最小请求间隔（上游限流严格，不建议低于 400ms）")
	dyttTimeout := flag.Duration("dytt-timeout", envDuration("HORIZON_DYTT_TIMEOUT", 5*time.Second), "单次抓取内容源的超时")
	dyttCallTimeout := flag.Duration("dytt-call-timeout", envDuration("HORIZON_DYTT_CALL_TIMEOUT", 12*time.Second), "一次抓取（含重试与退避）的总时限，必须小于客户端读超时")
	tmdbKey := flag.String("tmdb-api-key", envOr("HORIZON_TMDB_API_KEY", ""), "TMDb API Key（留空则关闭 TMDb 增强）")
	tmdbLang := flag.String("tmdb-language", envOr("HORIZON_TMDB_LANGUAGE", "zh-CN"), "TMDb 语言，如 zh-CN / en-US")
	translateOn := flag.Bool("translate", envOr("HORIZON_TRANSLATE_ENABLED", "true") != "false", "启用中文片名自动翻译（英文索引站点检索需要）")
	translateURL := flag.String("translate-url", envOr("HORIZON_TRANSLATE_URL", "https://transmart.qq.com/api/imt"), "中英翻译接口地址")
	translateTTL := flag.Duration("translate-cache-ttl", envDuration("HORIZON_TRANSLATE_TTL", 24*time.Hour), "翻译结果缓存时长")
	tpbBase := flag.String("tpb-base", envOr("HORIZON_TPB_BASE", "https://apibay.org"), "种子索引站点地址（The Pirate Bay 兼容 API）")
	csvBase := flag.String("csv-base", envOr("HORIZON_CSV_BASE", "https://torrents-csv.com"), "第二个种子索引源（torrents-csv 兼容 API）")
	csvOn := flag.Bool("csv", envOr("HORIZON_CSV_ENABLED", "true") != "false", "启用 torrents-csv 索引源（与 apibay 并发合并，结果更全）")
	dhtOn := flag.Bool("dht", envOr("HORIZON_DHT_ENABLED", "true") != "false", "启用 DHT（网络封锁 BT/UDP 时可关闭，仅靠 tracker 与 PEX 找 peer）")
	utpOn := flag.Bool("utp", envOr("HORIZON_UTP_ENABLED", "false") == "true", "启用 uTP（走 UDP；封锁 BT/UDP 的网络里会导致「连上 peer 但 0 字节」，默认关闭强制 TCP）")
	flag.Parse()

	if *jwtSecret == "" || *jwtSecret == "change-me" {
		log.Fatal("必须设置 JWT 密钥：-jwt-secret 或环境变量 HORIZON_JWT_SECRET（生产环境请用强随机值）")
	}

	// —— 存储层（SQLite 默认 / PostgreSQL 可选）——
	st, err := openStore(*dbType, *dsn, *pgDSN)
	if err != nil {
		log.Fatalf("初始化存储失败: %v", err)
	}
	defer st.Close()

	eng, err := engine.NewWithOptions(*dir, *maxConns, *listenPort, *dhtOn, *utpOn)
	if err != nil {
		log.Fatalf("初始化下载引擎失败: %v", err)
	}

	// —— 限流（Redis 优先，回退内存）——
	var rl ratelimit.Limiter
	if *redisAddr != "" {
		if r, err := ratelimit.NewRedisLimiter(*redisAddr, *redisPwd, 10, time.Minute); err == nil {
			rl = r
		} else {
			log.Printf("警告：Redis 限流不可用（%v），回退内存限流", err)
			rl = ratelimit.NewMemoryLimiter(10, time.Minute)
		}
	} else {
		rl = ratelimit.NewMemoryLimiter(10, time.Minute)
	}

	// —— S3 离线导出（可选）——
	var s3c *cloud.MinioS3
	if *s3Endpoint != "" {
		if c, err := cloud.NewMinioS3(*s3Endpoint, *s3Access, *s3Secret, *s3Bucket, *s3SSL); err == nil {
			s3c = c
		} else {
			log.Printf("警告：S3 初始化失败（%v），离线导出被禁用", err)
		}
	}

	am := auth.New(*jwtSecret)

	// —— 电影天堂元数据（封面 / 名称 / 简介）——
	// 抓取一律放在服务端：内容源对高频请求直接返回 400，客户端直连必定被限流；
	// 这里统一做限速、重试与缓存，客户端只消费 /api/v1/dytt/* 的结果。
	var dyttSvc *dytt.Service
	if *dyttOn {
		client := dytt.New(dytt.Config{
			BaseURL:     *dyttBase,
			Referer:     *dyttRef,
			MinInterval: *dyttInterval,
			Timeout:     *dyttTimeout,
			CallTimeout: *dyttCallTimeout,
		})
		dyttSvc = dytt.NewService(client, *dyttTTL)
		log.Printf("电影天堂元数据已启用：内容源 %s，缓存 %s，最小请求间隔 %s，单次超时 %s，总时限 %s",
			client.BaseURL(), *dyttTTL, *dyttInterval, *dyttTimeout, *dyttCallTimeout)
	} else {
		log.Printf("电影天堂元数据已禁用（HORIZON_DYTT_ENABLED=false），/api/v1/dytt/* 将返回 503")
	}

	// —— TMDb 增强（完全可选）——
	// 未配置 API Key 时 Enabled() 为 false，接口只返回电影天堂自身的元数据。
	var tmdbPrimary, tmdbFallback *tmdb.Client
	if strings.TrimSpace(*tmdbKey) != "" {
		tmdbPrimary = tmdb.New(tmdb.Config{APIKey: *tmdbKey, Language: *tmdbLang})
		if !strings.HasPrefix(*tmdbLang, "en") {
			// 冷门片常缺中文简介，用英文结果兜底补简介/封面
			tmdbFallback = tmdb.New(tmdb.Config{APIKey: *tmdbKey, Language: "en-US"})
		}
		log.Printf("TMDb 元数据增强已启用（语言 %s）", *tmdbLang)
	}

	// —— 种子检索：中文片名先翻译成英文再查 ——
	// 索引站点几乎只收英文片名，直接用中文查会返回满屏无关热门种子（实测搜「云雀叫天录」
	// 返回《蜘蛛侠》），翻译后检索才有效（「流浪地球」-> "The Wandering Earth" 实测 40 条全相关）。
	searcher := torsearch.New(torsearch.Config{
		TPBBase:          *tpbBase,
		CSVBase:          *csvBase,
		DisableCSV:       !*csvOn,
		TransmartURL:     *translateURL,
		DisableTranslate: !*translateOn,
		TranslateTTL:     *translateTTL,
	})
	torsearch.SetDefault(searcher)
	if *csvOn {
		log.Printf("第二索引源已启用（torrents-csv: %s），与 apibay 并发合并去重", *csvBase)
	} else {
		log.Printf("第二索引源已禁用（HORIZON_CSV_ENABLED=false），仅用 apibay，结果会偏少")
	}
	if *translateOn {
		log.Printf("中文片名自动翻译已启用（接口 %s，缓存 %s）", *translateURL, *translateTTL)
	} else {
		log.Printf("中文片名自动翻译已禁用，中文关键词将直接检索（结果可能不相关）")
	}
	if !*utpOn {
		log.Printf("uTP 已关闭（默认，强制 TCP）；UDP 干净的网络可设 HORIZON_UTP_ENABLED=true 启用")
	}
	if !*dhtOn {
		log.Printf("DHT 已关闭（HORIZON_DHT_ENABLED=false），仅靠 tracker 与 PEX 找 peer")
	}

	r := api.NewRouter(st, eng, am,
		api.WithRateLimiter(rl),
		api.WithDyttService(dyttSvc),
		api.WithTMDb(tmdbPrimary, tmdbFallback),
		api.WithTorrentSearcher(searcher),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := &http.Server{
		Addr:              *addr,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second, // 只限请求头，避免慢速攻击；不限 Body，否则会切断大文件下载
		IdleTimeout:       90 * time.Second, // keep-alive 空闲上限
	}
	go func() {
		log.Printf("地平线磁力云端服务已启动，监听 %s，下载目录 %s，数据库 %s", *addr, *dir, *dbType)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP 服务异常: %v", err)
		}
	}()

	// 后台任务统一挂 WaitGroup，退出时等它们收尾，避免边关边写数据库。
	var bgWG sync.WaitGroup
	if s3c != nil {
		bgWG.Add(1)
		go func() {
			defer bgWG.Done()
			runS3Worker(ctx, st, *dir, s3c)
		}()
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("收到退出信号，开始优雅关闭。")
	cancel()
	shCtx, shCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shCancel()
	_ = srv.Shutdown(shCtx)
	bgWG.Wait()
	eng.Close()
	log.Println("已退出")
}

// openStore 依据 db-type 构造存储层。
func openStore(dbType, dsn, pgDSN string) (store.Backend, error) {
	switch dbType {
	case "postgres":
		if pgDSN == "" {
			return nil, fmt.Errorf("db-type=postgres 需要 -postgres-dsn / HORIZON_POSTGRES_DSN")
		}
		return store.NewPostgres(pgDSN)
	default:
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			return nil, err
		}
		return store.New(db)
	}
}

// s3MaxRetries 单个任务上传 S3 的最大重试次数，超过后本轮不再重试，避免无限刷日志。
const s3MaxRetries = 5

// s3RetryBase 退避基数：第 n 次失败后等待 s3RetryBase * 2^(n-1)。
const s3RetryBase = 30 * time.Second

// runS3Worker 定时扫描已完成任务，上传到 S3 后写回预签名地址并标记状态。
// 失败按指数退避重试，超过 s3MaxRetries 次则放弃并在下次进程启动后才可能再试。
func runS3Worker(ctx context.Context, st store.Backend, dir string, s3c *cloud.MinioS3) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()

	type uploadState struct {
		fails int
		next  time.Time
	}
	states := map[string]*uploadState{}

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			tasks, err := st.ListAllTasks()
			if err != nil {
				log.Printf("S3 工作线程读取任务列表失败: %v", err)
				continue
			}
			seen := make(map[string]bool, len(tasks))
			for _, t := range tasks {
				if t.Status != "complete" {
					continue
				}
				seen[t.InfoHash] = true

				stt := states[t.InfoHash]
				if stt == nil {
					stt = &uploadState{}
					states[t.InfoHash] = stt
				}
				if stt.fails >= s3MaxRetries {
					continue
				}
				if !stt.next.IsZero() && time.Now().Before(stt.next) {
					continue
				}

				src := filepath.Join(dir, t.InfoHash)
				if t.Name != "" {
					src = filepath.Join(dir, t.Name)
				}
				url, err := cloud.UploadPath(ctx, s3c, src, "downloads/"+t.InfoHash, time.Hour)
				if err != nil {
					stt.fails++
					backoff := s3RetryBase << uint(stt.fails-1)
					if backoff > 30*time.Minute {
						backoff = 30 * time.Minute
					}
					stt.next = time.Now().Add(backoff)
					log.Printf("S3 上传失败（第 %d/%d 次，%s 后重试）%s: %v",
						stt.fails, s3MaxRetries, backoff, t.InfoHash, err)
					continue
				}
				delete(states, t.InfoHash)

				if err := st.SetStatus(t.OwnerID, t.InfoHash, "s3_uploaded"); err != nil {
					log.Printf("已上传 S3 但状态回写失败 %s: %v", t.InfoHash, err)
					continue
				}
				if err := st.Audit("system", "s3_upload", t.InfoHash, url); err != nil {
					log.Printf("S3 上传审计写入失败 %s: %v", t.InfoHash, err)
				}
				log.Printf("已上传到 S3：%s -> %s", t.InfoHash, url)
			}
			// 清理已不在列表中的任务状态，防止 map 无限增长。
			for k := range states {
				if !seen[k] {
					delete(states, k)
				}
			}
		}
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// envDuration 读取时长类环境变量，接受 "30m"、"400ms" 这类 Go duration 写法。
func envDuration(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
