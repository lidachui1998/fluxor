package core

import (
	"context"
	"fluxor/internal/config"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// 与内核 Unix socket 通信的 HTTP 客户端
var coreHTTPClient = &http.Client{
	Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			var conn net.Conn
			var lastErr error
			for attempt := 0; attempt < 3; attempt++ {
				conn, lastErr = net.Dial("unix", config.CoreSocket)
				if lastErr == nil {
					return conn, nil
				}
				if attempt < 2 {
					time.Sleep(time.Millisecond * 100)
				}
			}
			return nil, lastErr
		},
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     60 * time.Second,
	},
	Timeout: 90 * time.Second,
}

// cancelableReadCloser 在 Close 时释放 Context
type cancelableReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

// Close 先关闭底层响应体，再释放请求 Context。
// 这样可以保证响应流被完整读取后才触发 cancel，避免大 JSON 传输途中被截断。
func (c *cancelableReadCloser) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

// CoreRequest 向内核发送 HTTP 请求。
//
// 走的是 Unix Socket，内核对 unix 来源默认信任（实测带错密钥同样返回 200），
// 因此不附加 Authorization 头：panel_secret 只对内核的 TCP 外部控制端口生效。
func CoreRequest(method, path string, body io.Reader) (*http.Response, error) {
	// 动态超时：测速与提供商拉取为 90s，其余普通请求 10s
	timeout := 10 * time.Second
	if strings.Contains(path, "/healthcheck") || strings.Contains(path, "/providers/") {
		timeout = 90 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)

	url := "http://localhost" + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		cancel()
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := coreHTTPClient.Do(req)
	if err != nil {
		cancel()
		return nil, err
	}

	// 包装 Body 保证读取完毕后再释放 Context，防止流截断
	resp.Body = &cancelableReadCloser{
		ReadCloser: resp.Body,
		cancel:     cancel,
	}
	return resp, nil
}
