package dytt

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// 图床把 WebP 挂在 .jpg 地址上返回，且 Content-Type 谎报 image/jpeg
// （实测 18 张封面里 11 张如此）。.NET Framework 的 System.Drawing.Image
// 解不了 WebP，所以服务端必须转成 JPEG，否则客户端海报墙会大面积裂图。
//
// 样本是站点真实封面，避免自造 WebP 失真。
func TestFetchImageTranscodesWebPToJPEG(t *testing.T) {
	raw := loadFixtureBytes(t, "cover_sample.webp")
	if !bytes.HasPrefix(raw, []byte("RIFF")) || !bytes.Equal(raw[8:12], []byte("WEBP")) {
		t.Fatalf("样本 cover_sample.webp 不是 WebP，头字节 = % x", raw[:12])
	}

	client := New(Config{
		BaseURL:     "https://dytt.org.cn",
		MinInterval: 1,
		Retries:     1,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				// 撒谎：内容是 WebP，却声称 image/jpeg
				Header:  http.Header{"Content-Type": {"image/jpeg"}},
				Body:    io.NopCloser(bytes.NewReader(raw)),
				Request: r,
			}, nil
		})},
	})
	got, err := NewService(client, 0).FetchImage(context.Background(), "https://img.picbf.com/upload/a.jpg")
	if err != nil {
		t.Fatalf("FetchImage 失败: %v", err)
	}
	if got.ContentType != "image/jpeg" {
		t.Fatalf("ContentType = %q, want image/jpeg", got.ContentType)
	}
	if !bytes.HasPrefix(got.Body, []byte("\xff\xd8\xff")) {
		t.Fatalf("转码结果不是 JPEG，头字节 = % x", got.Body[:min(8, len(got.Body))])
	}
	img, err := jpeg.Decode(bytes.NewReader(got.Body))
	if err != nil {
		t.Fatalf("转码结果无法按 JPEG 解码: %v", err)
	}
	if b := img.Bounds(); b.Dx() <= 0 || b.Dy() <= 0 {
		t.Errorf("转码后尺寸异常: %v", b)
	}
	// 封面是竖版海报，转换后应保持竖版比例（没有被拉伸/旋转）
	if b := img.Bounds(); b.Dy() <= b.Dx() {
		t.Errorf("转码后应为竖版海报，实际 %dx%d", b.Dx(), b.Dy())
	}
}

// 正常 JPEG 不应被二次编码，避免画质与体积无谓损失。
func TestFetchImageLeavesJPEGUntouched(t *testing.T) {
	raw := makeJPEG(t, 40, 40)
	client := New(Config{
		BaseURL: "https://dytt.org.cn", MinInterval: 1, Retries: 1,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"image/jpeg"}},
				Body:       io.NopCloser(bytes.NewReader(raw)),
				Request:    r,
			}, nil
		})},
	})
	got, err := NewService(client, 0).FetchImage(context.Background(), "https://img.picbf.com/upload/b.jpg")
	if err != nil {
		t.Fatalf("FetchImage 失败: %v", err)
	}
	if got.ContentType != "image/jpeg" || !bytes.Equal(got.Body, raw) {
		t.Errorf("JPEG 被改动了：ct=%q 长度 %d -> %d", got.ContentType, len(raw), len(got.Body))
	}
}

// 转码失败（畸形 WebP）不能让整个请求失败，应安静退回原图。
func TestFetchImageToleratesBrokenWebP(t *testing.T) {
	broken := append([]byte("RIFF\x24\x00\x00\x00WEBPVP8 "), make([]byte, 32)...)
	client := New(Config{
		BaseURL: "https://dytt.org.cn", MinInterval: 1, Retries: 1,
		HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": {"image/webp"}},
				Body:       io.NopCloser(bytes.NewReader(broken)),
				Request:    r,
			}, nil
		})},
	})
	got, err := NewService(client, 0).FetchImage(context.Background(), "https://img.picbf.com/upload/c.webp")
	if err != nil {
		t.Fatalf("转码失败不应让请求失败: %v", err)
	}
	if got.ContentType != "image/webp" || !bytes.Equal(got.Body, broken) {
		t.Errorf("畸形 WebP 应原样返回，得到 ct=%q len=%d", got.ContentType, len(got.Body))
	}
}

// 非白名单域名必须拒绝，避免代理被当作 SSRF 跳板。
func TestFetchImageRejectsForeignHosts(t *testing.T) {
	svc := NewService(New(Config{MinInterval: 1, Retries: 1}), 0)
	for _, raw := range []string{
		"http://169.254.169.254/latest/meta-data/",
		"http://127.0.0.1:8080/a.jpg",
		"file:///c:/windows/win.ini",
		"",
	} {
		if _, err := svc.FetchImage(context.Background(), raw); err == nil {
			t.Errorf("FetchImage(%q) 应当报错，却成功了", raw)
		}
	}
	if !imageHostAllowed("cdn.img.picbf.com") {
		t.Error("白名单未覆盖子域名 cdn.img.picbf.com")
	}
}

func TestSniffImageContentType(t *testing.T) {
	cases := []struct {
		name string
		head []byte
		want string
	}{
		{"jpeg", []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01"), "image/jpeg"},
		{"png", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"), "image/png"},
		{"gif87", []byte("GIF87a\x01\x00\x01\x00\x80\x00\x00"), "image/gif"},
		{"gif89", []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00"), "image/gif"},
		{"bmp", []byte("BM\x36\x00\x00\x00\x00\x00\x00\x00\x36\x00\x00\x00"), "image/bmp"},
		// WebP 有三种 chunk 布局，嗅探只看 RIFF/WEBP 标记，三种都必须认出来。
		// 旧实现在这里漏判会让客户端拿到 WebP 而 Image.FromStream 抛异常、封面裂图。
		{"webp simple (VP8)", []byte("RIFF\x24\x00\x00\x00WEBPVP8 \x18\x00\x00\x00"), "image/webp"},
		{"webp lossless (VP8L)", []byte("RIFF\x24\x00\x00\x00WEBPVP8L\x18\x00\x00\x00"), "image/webp"},
		{"webp extended (VP8X)", []byte("RIFF\x24\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00"), "image/webp"},
		// RIFF 容器但不是 WebP（例如 WAV）不应被当成图片
		{"riff but not webp", []byte("RIFF\x24\x00\x00\x00WAVElfmt "), ""},
		{"avif", []byte("\x00\x00\x00\x20ftypavif\x00\x00\x00\x00"), "image/avif"},
		{"heic", []byte("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00"), "image/heic"},
		{"unknown", []byte("this is not an image at all!"), ""},
		{"too short", []byte("RIFF"), ""},
	}
	for _, c := range cases {
		if got := sniffImageContentType(c.head); got != c.want {
			t.Errorf("%s: sniffImageContentType = %q, want %q", c.name, got, c.want)
		}
	}
}

// ============ 辅助 ============

func loadFixtureBytes(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("读取样本失败 %s: %v", name, err)
	}
	return b
}

func makeJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 3), G: uint8(y * 5), B: 180, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("生成 JPEG 失败: %v", err)
	}
	return buf.Bytes()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
