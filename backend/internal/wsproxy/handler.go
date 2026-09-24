package wsproxy

import (
	"context"
	"fluxor/internal/config"
	"fluxor/internal/logx"
	"github.com/gorilla/websocket"
	"net"
	"net/http"
)

// WsProxyHandler 保持不变
func WsProxyHandler(targetPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			// 带上 Origin 与 Host：升级失败最常见的原因是「反代没有透传/改写了 Host」，
			// 导致同源校验不过。只打一句 "origin not allowed" 用户无从判断该怎么改。
			logx.Error(logx.ModuleWS, "failed to upgrade websocket connection: path=%s origin=%q host=%q err=%v",
				targetPath, r.Header.Get("Origin"), r.Host, err)
			return
		}
		defer conn.Close()

		dialer := &websocket.Dialer{
			NetDialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				return net.Dial("unix", config.CoreSocket)
			},
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		}
		path := targetPath
		if r.URL.RawQuery != "" {
			path += "?" + r.URL.RawQuery
		}
		// 内核对 Unix Socket 来源默认信任，无需 Bearer 认证头
		coreConn, _, err := dialer.Dial("ws://localhost"+path, nil)
		if err != nil {
			// 内核未运行是预期情况，因此默认级别下不打扰用户；但这条路径此前完全静默，
			// 一旦出现「连接/日志/流量全空」，就无从区分是前端没连上、来源校验拒了、
			// 还是内核侧拨号失败。用 Debug 级留一条线索（FLUXOR_LOG_LEVEL=debug 可见）。
			logx.Debug(logx.ModuleWS, "core side websocket dial failed: path=%s err=%v", path, err)
			return
		}
		defer coreConn.Close()

		errChan := make(chan error, 2)

		go func() {
			for {
				msgType, msg, err := coreConn.ReadMessage()
				if err != nil {
					errChan <- err
					return
				}
				if err := conn.WriteMessage(msgType, msg); err != nil {
					errChan <- err
					return
				}
			}
		}()

		go func() {
			for {
				msgType, msg, err := conn.ReadMessage()
				if err != nil {
					errChan <- err
					return
				}
				if err := coreConn.WriteMessage(msgType, msg); err != nil {
					errChan <- err
					return
				}
			}
		}()

		<-errChan
	}
}
