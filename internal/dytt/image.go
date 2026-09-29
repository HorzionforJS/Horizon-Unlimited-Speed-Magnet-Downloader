package dytt

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image/jpeg"
	"io"
	"net/http"
	"strings"
	"time"

	"horizon/internal/guard"

	// 图床大量使用 WebP，而 .NET Framework 的 System.Drawing.Image
	// 无法解码 WebP，所以转换必须在服务端做掉。
	"golang.org/x/image/webp"
)

// maxImageBytes 限制单张封面大小，避免被当作流量放大器。
const maxImageBytes = 12 << 20

// maxDecodePixels 限制可转换图片的像素总量：WebP 头部可以声称任意尺寸，
// 不设上限时一张几十字节的恶意图就能让服务端分配几个 GB。
const maxDecodePixels = 40 << 20 // 约 4000x10000

// webpToJPEGQuality 是 WebP 转码质量。源图多为 270x383 的封面，
// 质量 85 下体积与 WebP 接近，客户端拿到的却是通用可解码的 JPEG。
const webpToJPEGQuality = 85

// allowedImageHosts 是放行代理的图床白名单。
// 这些域名来自站点实际返回的封面地址（img.picbf.com 等）。
// 用白名单而非“任意 URL”可以避免代理被滥用为 SSRF 跳板。
var allowedImageHosts = []string{
	"img.picbf.com",
	"img.lzipic.com",
	"img.lzzyimg.com",
	"img.bfzypic.com",
	"pic.youkupic.com",
}

// ImageResult 是封面代理结果。
type ImageResult struct {
	Body        []byte
	ContentType string
}

// ErrImageHostNotAllowed 表示封面域名不在白名单内。
var ErrImageHostNotAllowed = errors.New("不允许代理该域名的图片")

// ErrInvalidImageURL 表示封面地址本身不合法（空值或非 http/https 协议）。
// 与 ErrImageHostNotAllowed 一样属于调用方参数问题，接口层应回 400 而非 502。
var ErrInvalidImageURL = errors.New("图片地址不合法")

// FetchImage 代理下载封面图。
// 旧版客户端会带 Referer 直接引用图床导致 403/裂图，走后端代理可稳定显示；
// 同时把 WebP 等浏览器友好、而 .NET 老版本 Image 不支持的格式交给客户端自行处理。
func (s *Service) FetchImage(ctx context.Context, rawURL string) (ImageResult, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return ImageResult{}, fmt.Errorf("%w: 缺少图片地址", ErrInvalidImageURL)
	}
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		return ImageResult{}, fmt.Errorf("%w: 仅支持 http/https 协议", ErrInvalidImageURL)
	}
	// 两道校验都必须有：
	//  1. 域名后缀白名单——限定「允许代理哪些图床」；
	//  2. guard.ValidateURL——把解析到的实际 IP 也过一遍内网/保留段检查。
	// 只做 1 的话，白名单里的第三方图床一旦被 DNS 指向内网，
	// 这个接口立刻变成打内网的跳板（白名单域名并不受我们控制）。
	host := hostOf(rawURL)
	if !imageHostAllowed(host) {
		return ImageResult{}, fmt.Errorf("%w: %s", ErrImageHostNotAllowed, host)
	}
	if err := guard.ValidateURL(rawURL); err != nil {
		return ImageResult{}, fmt.Errorf("%w: %s", ErrInvalidImageURL, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return ImageResult{}, err
	}
	// 图床对空 Referer 与同源 Referer 都放行，这里只带 UA 与 Referer
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", s.client.cfg.Referer)
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,*/*;q=0.8")
	// 必须禁用压缩：下面的格式嗅探依赖原始 magic bytes，
	// gzip 之后既认不出 WebP 也没法转码成 JPEG，客户端会直接裂图。
	req.Header.Set("Accept-Encoding", "identity")

	client := s.client.cfg.HTTPClient
	if client == nil {
		client = guard.SafeHTTPClient(15*time.Second, 3)
	}
	resp, err := client.Do(req)
	if err != nil {
		return ImageResult{}, fmt.Errorf("拉取图片失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return ImageResult{}, fmt.Errorf("图床返回 HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes))
	if err != nil {
		return ImageResult{}, fmt.Errorf("读取图片失败: %w", err)
	}
	// 必须以实际字节为准：图床把 WebP 内容挂在 .jpg 地址上返回，
	// 且 Content-Type 也谎报成 image/jpeg（实测 18 张里 11 张如此）。
	// 只有嗅探真实格式，才知道该不该转码。
	ct := sniffImageContentType(body)
	if ct == "" {
		// 字节无法识别时，再退回上游声明，最后兜底 image/jpeg
		if declared := resp.Header.Get("Content-Type"); strings.HasPrefix(declared, "image/") {
			ct = declared
		} else {
			ct = "image/jpeg"
		}
	}

	// WebP 转成 JPEG 再下发：客户端是 .NET Framework WinForms，
	// System.Drawing.Image 解不了 WebP（实测抛 ArgumentException），
	// 在这里转掉可以避免每个客户端都要装解码器。
	if ct == "image/webp" {
		if out, err := webpToJPEG(body); err == nil {
			return ImageResult{Body: out, ContentType: "image/jpeg"}, nil
		}
		// 转码失败不致命：原样返回 WebP，总比 502 让客户端裂图好
	}
	return ImageResult{Body: body, ContentType: ct}, nil
}

// webpToJPEG 把 WebP 解码后重新编码为 JPEG。
// 先读头部尺寸做上限校验，避免畸形图片触发超大内存分配。
func webpToJPEG(body []byte) ([]byte, error) {
	cfg, err := webp.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("解析 WebP 头失败: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > maxDecodePixels {
		return nil, fmt.Errorf("WebP 尺寸超限: %dx%d", cfg.Width, cfg.Height)
	}
	src, err := webp.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("解码 WebP 失败: %w", err)
	}
	var buf bytes.Buffer
	buf.Grow(len(body) * 2)
	if err := jpeg.Encode(&buf, src, &jpeg.Options{Quality: webpToJPEGQuality}); err != nil {
		return nil, fmt.Errorf("编码 JPEG 失败: %w", err)
	}
	return buf.Bytes(), nil
}

// sniffImageContentType 依据文件头判断真实图片格式，识别不出返回空字符串。
func sniffImageContentType(body []byte) string {
	if len(body) < 12 {
		return ""
	}
	switch {
	case bytes.HasPrefix(body, []byte("\xff\xd8\xff")):
		return "image/jpeg"
	case bytes.HasPrefix(body, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(body, []byte("GIF87a")), bytes.HasPrefix(body, []byte("GIF89a")):
		return "image/gif"
	case bytes.HasPrefix(body, []byte("BM")):
		return "image/bmp"
	// WebP 是 RIFF 容器：RIFF????WEBP
	case bytes.HasPrefix(body, []byte("RIFF")) && bytes.Equal(body[8:12], []byte("WEBP")):
		return "image/webp"
	// AVIF/HEIF 也是 ftyp 容器，图床偶有使用
	case bytes.Equal(body[4:8], []byte("ftyp")):
		if bytes.Equal(body[8:12], []byte("avif")) || bytes.Equal(body[8:12], []byte("avis")) {
			return "image/avif"
		}
		return "image/heic"
	}
	return ""
}

func hostOf(rawURL string) string {
	s := rawURL
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.Index(s, ":"); i >= 0 {
		s = s[:i]
	}
	return strings.ToLower(s)
}

func imageHostAllowed(host string) bool {
	for _, allowed := range allowedImageHosts {
		if host == allowed || strings.HasSuffix(host, "."+allowed) {
			return true
		}
	}
	return false
}
