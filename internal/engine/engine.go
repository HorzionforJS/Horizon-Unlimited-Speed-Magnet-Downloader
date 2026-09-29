package engine

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
)

// Progress 为一次任务的实时快照。
type Progress struct {
	InfoHash  string `json:"info_hash"`
	Name      string `json:"name"`
	Status    string `json:"status"` // resolving / active / complete / paused
	Total     int64  `json:"total"`
	Completed int64  `json:"completed"`
	Peers     int    `json:"peers"`
	Speed     int64  `json:"speed"` // 字节/秒
}

type rateSample struct {
	bytes int64
	at    time.Time
}

type Engine struct {
	client   *torrent.Client
	dir      string
	maxConns int
	trackers [][]string

	mu   sync.Mutex
	last map[string]rateSample
}

// DefaultTrackers 默认 Tracker 分层列表：裸磁力（不带 tr= 参数）仅靠 DHT 发现 peer 很慢，
// 补一层 tracker 可显著加快元数据解析与找种。同一层（内层 slice）内 tracker 并行通告。
//
// 分层依据是实测结果：部分网络会针对性封锁 BT 的 UDP 端口（实测 UDP tracker 与 DHT
// 引导节点全部无回包，而普通 UDP 出站正常），此时只有 HTTP/HTTPS tracker 可用。
// 因此把 HTTP/HTTPS 放在第一层，UDP 放第二层作为补强，保证在两种网络下都能找到 peer。
var DefaultTrackers = [][]string{
	// 第一层：HTTP/HTTPS tracker（不受 UDP 封锁影响，可用性最稳）
	{
		"https://tracker.foreverpirates.co:443/announce", // 实测有效：能返回 peer 与种子数
		"http://tracker.openbittorrent.com:80/announce",
		"http://tracker.bittor.pw:1337/announce",
		"https://tracker.zhuqiy.com/announce",
		"https://tracker.tamersunion.org/announce",
	},
	// 第二层：UDP tracker（网络未封锁 UDP 时补强，能显著提升找种速度）
	{
		"udp://tracker.opentrackr.org:1337/announce",
		"udp://open.tracker.cl:1337/announce",
		"udp://open.demonii.com:1337/announce",
		"udp://tracker.torrent.eu.org:451/announce",
		"udp://tracker.openbittorrent.com:6969/announce",
		"udp://exodus.desync.com:6969/announce",
		"udp://tracker.dler.org:6969/announce",
	},
}

// New 创建下载引擎。anacrolix 默认开启 DHT/PEX/uTP/协议加密。
// listenPort 为 BT/TCP 与 DHT/UDP 共用的固定监听端口：部署方需在防火墙/安全组开放该端口（TCP+UDP）；
// 传 0 表示由系统随机分配（公网/严格防火墙下端口不可达，会难以连上 peer）。
func New(dir string, maxConns int, listenPort int) (*Engine, error) {
	return NewWithOptions(dir, maxConns, listenPort, true, false)
}

// NewWithDHT 与 New 相同，但可显式控制是否启用 DHT。
func NewWithDHT(dir string, maxConns int, listenPort int, enableDHT bool) (*Engine, error) {
	return NewWithOptions(dir, maxConns, listenPort, enableDHT, false)
}

// NewWithOptions 创建下载引擎，可分别控制 DHT 与 uTP。
//
// enableDHT=false：DHT 走 UDP，部分网络会封锁 BT 的 UDP 流量，此时 DHT 只带来无谓等待。
// enableUTP=false（默认）：uTP 同样走 UDP。实测在封锁 BT/UDP 的网络里，uTP 会让
// 连接数与种子数大幅缩水（同一 magnet 实测：开 uTP 3 连接/0 种子，关 uTP 7 连接/6 种子），
// 因为握手能走 TCP、数据阶段切到 UDP 后即被丢包，表现为「连上了但 0 字节」。
// 强制 TCP 最稳，故默认关闭；在 UDP 干净的网络可传 true 以利用 uTP 的穿透优势。
func NewWithOptions(dir string, maxConns int, listenPort int, enableDHT bool, enableUTP bool) (*Engine, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dir
	cfg.Seed = false            // 纯离线下载，不做种
	cfg.NoUpload = true         // 关闭上传（离线下载场景）
	cfg.ListenPort = listenPort // 固定监听端口
	cfg.NoDHT = !enableDHT
	cfg.DisableUTP = !enableUTP
	client, err := torrent.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &Engine{client: client, dir: dir, maxConns: maxConns, trackers: DefaultTrackers, last: map[string]rateSample{}}, nil
}

func (e *Engine) Dir() string { return e.dir }

// FileEntry 描述已完成任务在磁盘上的一个文件。
type FileEntry struct {
	Name string // 相对下载目录的路径（多文件含 torrent 名目录，'/' 分隔）
	Path string // 磁盘绝对路径
}

// FileEntries 返回任务在磁盘上的所有文件（供「取回文件」流式返回 / 打包 zip）。
func (e *Engine) FileEntries(infoHash string) (torrentName string, entries []FileEntry, err error) {
	t, err := e.find(infoHash)
	if err != nil {
		return "", nil, err
	}
	if info := t.Info(); info != nil {
		torrentName = info.Name
	}
	for _, f := range t.Files() {
		rel := f.Path()
		entries = append(entries, FileEntry{Name: rel, Path: filepath.Join(e.dir, filepath.FromSlash(rel))})
	}
	return torrentName, entries, nil
}

// AddMagnet 添加磁力链接，立即返回 info hash；元数据（文件名/大小）由 DHT/Tracker 异步解析。
func (e *Engine) AddMagnet(magnetURI string) (string, error) {
	t, err := e.client.AddMagnet(magnetURI)
	if err != nil {
		return "", err
	}
	t.SetMaxEstablishedConns(e.maxConns)
	if len(e.trackers) > 0 {
		t.AddTrackers(e.trackers)
	}
	infoHash := t.InfoHash().HexString()
	go func() {
		<-t.GotInfo()
		e.declareDownload(t)
	}()
	return infoHash, nil
}

// declareDownload 声明「下载这个 torrent 的全部内容」。
//
// 必须显式声明：anacrolix 的 AddMagnet 只创建 torrent 并去解析元数据，新 torrent 的
// piece 优先级初始是 PiecePriorityNone（不想要）。少了这一步，任务会稳定停在
// 「已连上 peer、元数据也拿到了，但 0 字节」——实测就是这样 6 分钟 0 字节。
//
// 另外 DownloadAll() 内部会读 t.info.NumPieces()，元数据没到时调用会空指针 panic
// （上游注释：Marks the entire torrent for download. Requires the info first,
// see GotInfo）。所以调用方必须保证元数据已就绪；AddMagnet 是在 <-t.GotInfo()
// 之后调用本方法的。
func (e *Engine) declareDownload(t *torrent.Torrent) {
	if t.Info() == nil {
		return
	}
	t.SetMaxEstablishedConns(e.maxConns)
	t.DownloadAll()
}

// Progress 返回任务的实时进度，并顺带计算下载速度（两次调用间差值）。
func (e *Engine) Progress(infoHash string) (*Progress, error) {
	t, err := e.find(infoHash)
	if err != nil {
		return nil, err
	}
	p := e.snapshot(t)

	now := time.Now()
	e.mu.Lock()
	prev, ok := e.last[infoHash]
	if ok && now.After(prev.at) {
		if dt := now.Sub(prev.at).Seconds(); dt > 0 {
			spd := int64(float64(p.Completed-prev.bytes) / dt)
			if spd > 0 {
				p.Speed = spd
			}
		}
	}
	e.last[infoHash] = rateSample{bytes: p.Completed, at: now}
	e.mu.Unlock()
	return p, nil
}

func (e *Engine) snapshot(t *torrent.Torrent) *Progress {
	p := &Progress{InfoHash: t.InfoHash().HexString(), Status: "resolving"}
	if info := t.Info(); info != nil {
		p.Name = info.Name
		p.Total = info.TotalLength()
		p.Status = "active"
	}
	p.Completed = t.BytesCompleted()
	if p.Total > 0 && p.Completed >= p.Total {
		p.Status = "complete"
	}
	p.Peers = len(t.PeerConns())
	return p
}

func (e *Engine) Pause(infoHash string) error {
	t, err := e.find(infoHash)
	if err != nil {
		return err
	}
	// 连接数与 piece 优先级一起停：只停连接数时 tracker/DHT 仍会重新连上其他 peer，
	// 严格来说不算暂停。NumPieces() 同样要求元数据已就绪，故加判空。
	t.SetMaxEstablishedConns(0)
	if t.Info() != nil {
		t.CancelPieces(0, t.NumPieces())
	}
	return nil
}

func (e *Engine) Resume(infoHash string) error {
	t, err := e.find(infoHash)
	if err != nil {
		return err
	}
	t.SetMaxEstablishedConns(e.maxConns)
	if t.Info() != nil {
		t.DownloadAll()
	}
	return nil
}

func (e *Engine) Remove(infoHash string) error {
	t, err := e.find(infoHash)
	if err != nil {
		return err
	}
	t.Drop()
	return nil
}

func (e *Engine) Close() {
	if e.client == nil {
		return
	}
	for _, t := range e.client.Torrents() {
		t.Drop()
	}
	e.client.Close()
}

// AllInfoHashes 返回引擎中当前所有 torrent 的 info hash（供黑名单扫描）。
func (e *Engine) AllInfoHashes() []string {
	var out []string
	for _, t := range e.client.Torrents() {
		out = append(out, t.InfoHash().HexString())
	}
	return out
}

func (e *Engine) find(infoHash string) (*torrent.Torrent, error) {
	for _, t := range e.client.Torrents() {
		if t.InfoHash().HexString() == infoHash {
			return t, nil
		}
	}
	return nil, errors.New("torrent not found")
}
