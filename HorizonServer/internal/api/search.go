package api

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"horizon/internal/torsearch"
)

//go:embed web/index.html
var webIndex string

const tpbBase = "https://apibay.org"

// fetchTPB 从 The Pirate Bay 公开 API（apibay.org）抓取种子列表。
// endpoint 形如 "q.php?q=…&cat=0" 或 "precompiled/data_top100_recent.json"。
func fetchTPB(endpoint string) ([]interface{}, error) {
	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequest(http.MethodGet, tpbBase+"/"+endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "horizon-downloader/1.0")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("上游返回 HTTP %d", resp.StatusCode)
	}
	var arr []interface{}
	if err := json.Unmarshal(body, &arr); err != nil {
		// apibay 偶发返回对象或异常内容，按“无结果”处理
		return []interface{}{}, nil
	}
	return arr, nil
}

// searchTorrents 种子搜索代理（公开接口，无需登录）。
//
// 走 torsearch：中文关键词先翻译成英文片名再检索，并过滤掉与片名无关的结果。
// 直接用中文词查 apibay 会返回满屏无关热门种子（实测搜「云雀叫天录」返回《蜘蛛侠》），
// 翻译后检索才有效（「流浪地球」-> "The Wandering Earth" 实测 40 条全相关）。
func (s *Server) searchTorrents(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	if q == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少搜索关键词 q"})
		return
	}
	cat := strings.TrimSpace(c.Query("cat"))
	searcher := s.torsearch
	if searcher == nil {
		searcher = torsearch.Default()
	}
	// limit 是客户端忽略不了的成本：每多返回一条，就多一条片名要翻译。
	// 不接 limit 时搜「avatar」会回 200 条、耗时 10 秒以上，而用户从来翻不到第 200 条。
	limit := 0
	if v := strings.TrimSpace(c.Query("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 40*time.Second)
	defer cancel()
	res, err := searcher.SearchLimit(ctx, q, cat, limit)
	if err != nil {
		if errors.Is(err, torsearch.ErrEmptyQuery) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "缺少搜索关键词 q"})
			return
		}
		c.JSON(http.StatusBadGateway, gin.H{"error": "搜索失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"results":       res.Results,
		"query":         res.Query,
		"search_term":   res.SearchTerm,
		"translated":    res.Translated,
		"translated_ok": res.TranslatedOK,
		"filtered_out":  res.FilteredOut,
		"name_zh_count": res.NameZhCount,
		// sources / source_errors 用于排查「数量变少」：是索引源挂了，还是这个
		// 关键词本来就没片源。客户端把 source_errors 转成提示给用户看。
		"sources":       res.Sources,
		"source_errors": res.SourceErrors,
		"hint":          res.Hint,
		"count":         len(res.Results),
	})
}

// topTorrents 近期/热门种子（公开接口，无需登录）。
func (s *Server) topTorrents(c *gin.Context) {
	arr, err := fetchTPB("precompiled/data_top100_recent.json")
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "获取失败: " + err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"results": arr})
}

// serveIndex 返回种子搜索 Web 页。
func serveIndex(c *gin.Context) {
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, webIndex)
}
