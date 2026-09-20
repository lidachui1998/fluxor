package wsproxy

import (
	"context"
	"fluxor/internal/config"
	"github.com/gorilla/websocket"
	"log"
	"net"
	"net/http"
)

// WsProxyHandler 保持不变
func WsProxyHandler(targetPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Printf("[WS] 升级失败 (路径 %s): %v", targetPath, err)
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
			// 内核未运行或连接失败是预期情况，不记录日志
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
