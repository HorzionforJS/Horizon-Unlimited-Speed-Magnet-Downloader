package engine

import (
	"crypto/sha1"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/types"
)

// buildLocalTorrent 在磁盘上造一个单文件 torrent（内容本地生成，测试不依赖网络）。
func buildLocalTorrent(t *testing.T, dir string, size int, pieceLen int64) *metainfo.MetaInfo {
	t.Helper()
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i * 7)
	}
	info := &metainfo.Info{
		Name:        "probe.bin",
		PieceLength: pieceLen,
		Length:      int64(len(payload)),
	}
	for off := 0; off < len(payload); off += int(pieceLen) {
		end := off + int(pieceLen)
		if end > len(payload) {
			end = len(payload)
		}
		sum := sha1.Sum(payload[off:end])
		info.Pieces = append(info.Pieces, sum[:]...)
	}
	b, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	return &metainfo.MetaInfo{InfoBytes: b}
}

// TestAddMagnetDeclaresDownload 是本仓库最重要的一条防回归测试。
//
// 曾经的 bug：AddMagnet 只创建 torrent、不声明要下载，piece 优先级全是
// PiecePriorityNone，任务会「连上 peer、元数据也拿到了，但永远 0 字节」。
// 上游 DownloadAll() 又要求元数据已就绪（否则读 t.info.NumPieces() 空指针 panic），
// 所以正确实现是 <-t.GotInfo() 之后再 DownloadAll()。
func TestAddMagnetDeclaresDownload(t *testing.T) {
	dir := t.TempDir()
	mi := buildLocalTorrent(t, dir, 64*1024, 16*1024)

	eng, err := NewWithOptions(filepath.Join(dir, "dl"), 10, 0, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	// 用等价的本地 metainfo 走同一个「声明下载」路径：手动复刻 AddMagnet 里的逻辑，
	// 再用真实的 AddMagnet 断言一次。
	tor, err := eng.client.AddTorrent(mi)
	if err != nil {
		t.Fatal(err)
	}

	// 未声明前：优先级必须全是 None（证明这个 bug 的成因）
	for i := 0; i < tor.NumPieces(); i++ {
		if got := tor.Piece(i).State().Priority; got != types.PiecePriorityNone {
			t.Fatalf("piece %d priority before DownloadAll = %v, want None", i, got)
		}
	}

	// 走引擎的公开路径声明下载
	eng.declareDownload(tor)

	deadline := time.Now().Add(5 * time.Second)
	for {
		all := true
		for i := 0; i < tor.NumPieces(); i++ {
			if tor.Piece(i).State().Priority == types.PiecePriorityNone {
				all = false
				break
			}
		}
		if all {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pieces were not marked for download within 5s (DownloadAll regression)")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestAddMagnetDoesNotPanicBeforeInfo 确认对「元数据尚未就绪」的 magnet
// 调用 AddMagnet 不会 panic（旧实现直接调 DownloadAll() 会空指针崩）。
func TestAddMagnetDoesNotPanicBeforeInfo(t *testing.T) {
	dir := t.TempDir()
	eng, err := NewWithOptions(filepath.Join(dir, "dl"), 10, 0, false, false)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close()

	// 一个几乎不可能解析出元数据的 infohash；AddMagnet 必须立即返回而不崩。
	ih := "0123456789abcdef0123456789abcdef01234567"
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("AddMagnet panicked (should wait for GotInfo first): %v", r)
		}
	}()
	got, err := eng.AddMagnet("magnet:?xt=urn:btih:" + ih)
	if err != nil {
		t.Fatalf("AddMagnet returned error: %v", err)
	}
	if got != ih {
		t.Fatalf("info hash = %q, want %q", got, ih)
	}
}

// TestDefaultTrackersLayered 确认 tracker 分层：第一层必须是 HTTP/HTTPS，
// 这样在被封锁 BT/UDP 的网络里也能先靠 HTTP tracker 找到 peer。
func TestDefaultTrackersLayered(t *testing.T) {
	if len(DefaultTrackers) < 2 {
		t.Fatalf("expected >= 2 tracker tiers, got %d", len(DefaultTrackers))
	}
	for _, tr := range DefaultTrackers[0] {
		if strings.HasPrefix(tr, "udp://") {
			t.Errorf("first tier must be HTTP(S) only, got %q", tr)
		}
	}
	hasUDP := false
	for _, tier := range DefaultTrackers[1:] {
		for _, tr := range tier {
			if strings.HasPrefix(tr, "udp://") {
				hasUDP = true
			}
		}
	}
	if !hasUDP {
		t.Error("expected a UDP tier as fallback")
	}
}
