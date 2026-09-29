package dytt

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

// mockHTTP 返回一个按路径映射响应的 http.Client，并统计真实请求次数。
// 路径不存在时返回 404，用于验证错误分支。
func mockHTTP(t *testing.T, calls *int, routes map[string]string) *http.Client {
	t.Helper()
	return &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			*calls++
			for path, body := range routes {
				if strings.HasSuffix(r.URL.Path, path) {
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     http.Header{"Content-Type": {"text/html; charset=utf-8"}},
						Body:       io.NopCloser(strings.NewReader(body)),
						Request:    r,
					}, nil
				}
			}
			return &http.Response{
				StatusCode: http.StatusNotFound,
				Header:     http.Header{"Content-Type": {"text/html"}},
				Body:       io.NopCloser(strings.NewReader("404 Not Found")),
				Request:    r,
			}, nil
		}),
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
