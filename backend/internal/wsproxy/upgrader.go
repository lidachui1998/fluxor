package wsproxy

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/gorilla/websocket"
)

// upgrader 面板与内核之间的 WebSocket 转发入口。
//
// CheckOrigin 必须做来源校验，不能放行所有来源：
//   - 面板自身没有任何认证（设计如此，靠「只监听回环」与反代承担访问控制）；
//   - WebSocket **不受 CORS 约束**，若不校验来源，用户访问的任意网页都能连上
//     /connections、/logs（日志可能含订阅地址与节点信息）、/traffic 读取内容。
var upgrader = websocket.Upgrader{
	CheckOrigin:     checkSameOrigin,
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

// checkSameOrigin 校验 WebSocket 升级请求是否来自面板自己的页面。
//
// 判定分两层，顺序有意如此：
//
//  1. **Sec-Fetch-Site**（首选）。它由浏览器按「发起方页面 ↔ 目标 URL」自行推导，
//     **不受反向代理改写 Host 影响**，因此是唯一在代理部署下依然可靠的信号：
//     cross-site 直接拒绝，same-origin / same-site / none 放行。
//     该头缺失（老浏览器、非浏览器客户端）时进入第 2 层。
//
//  2. **主机名比较**（退化路径）。只比主机名、**不比端口**，因为反代常把 Host 的端口
//     剥掉。实测（fnOS 应用网关，面板挂在 https://<host>:3333 之后）：
//
//     Origin: https://fn.1kw.site:3333
//     Host:   fn.1kw.site
//
//     逐一比较「主机:端口」会把这种正常部署整体拒掉——四路 WebSocket（traffic /
//     memory / connections / logs）同时 403，面板表现为「连接、日志、实时流量全空」。
//     按主机名比较仍然拦得住真正的跨站来源（攻击页面在别的主机名下），而同主机不同
//     端口在浏览器的同源策略里本就属于同一个 site；能占用本机其它端口的攻击者已经
//     在本机之内，不是这道校验的防御目标。
//
//     反代若把 Host 改写成上游名（如 localhost），再尝试 X-Forwarded-Host（网关配置
//     正确时会透传），它代表浏览器实际使用的主机名。
//
// 浏览器发起的 WebSocket 一定带 Origin；缺失说明是脚本 / curl 这类非浏览器客户端
// （不构成跨站请求伪造），放行。`Origin: null`（沙箱 iframe / file://）解析后主机名
// 为空，会被拒绝。
func checkSameOrigin(r *http.Request) bool {
	// 第 0 层：显式放行清单（见 allowedOrigins 的说明）。
	// 放在最前面：它是「自动判定不适用」时的补救手段。
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	originHost := ""
	if u, err := url.Parse(origin); err == nil {
		originHost = hostName(u.Host)
	}
	if originHost != "" && originExplicitlyAllowed(originHost) {
		return true
	}

	// 第 1 层：浏览器自报的跨站属性（代理改写 Host 也不影响它）
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site"))) {
	case "cross-site":
		return false
	case "same-origin", "same-site", "none":
		return true
	}

	// 第 2 层：退化为主机名比较
	if originHost == "" {
		return false
	}
	if strings.EqualFold(originHost, hostName(r.Host)) {
		return true
	}
	if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
		// 可能是 "a.example, b.example" 这样的多级链，取最靠前的一个
		first := strings.TrimSpace(strings.Split(fwd, ",")[0])
		if strings.EqualFold(originHost, hostName(first)) {
			return true
		}
	}
	return false
}

// allowedOrigins 是来源校验的**显式放行清单**，来自环境变量
// FLUXOR_WS_ALLOWED_ORIGINS：
//
//   - 未设置（默认）：只用上面两层自动判定；
//   - "*"：放行一切来源（等于关掉这道校验，仅在网络边界确实安全时使用）；
//   - 逗号分隔的主机名：**额外**放行这些主机名（不是白名单——未命中时仍走自动判定，
//     这样设置它不会把本来可用的部署弄坏）。
//
// 为什么需要它：自动判定的第 2 层依赖「反代把浏览器实际使用的主机名传下来」。若某个
// 网关把 Host 改写成与访问地址无关的值（如上游名 localhost）且不透传
// X-Forwarded-Host，我们就无从得知浏览器看到的是哪个主机名——这类部署会在此校验
// 生效后突然失去全部实时数据（traffic / memory / connections / logs 四路 403），
// 而用户手上没有任何补救手段。留一个逃生口比让这类部署无解更负责任。
//
// 取值在进程启动时读一次即可（部署后改环境变量本就需要重启面板）。
var allowedOrigins = parseAllowedOrigins(os.Getenv("FLUXOR_WS_ALLOWED_ORIGINS"))

func parseAllowedOrigins(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if p := strings.ToLower(strings.TrimSpace(part)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// originExplicitlyAllowed 判定主机名是否在显式放行清单里。
func originExplicitlyAllowed(host string) bool {
	for _, a := range allowedOrigins {
		if a == "*" || strings.EqualFold(a, host) {
			return true
		}
	}
	return false
}

// hostName 从 "host[:port]" 中取出主机名；IPv6 字面量的方括号也一并去掉。
//
// 反代可能剥掉端口，因此 "host" 与 "[::1]" 这两种不带端口的形式都要能正确解析。
func hostName(hostPort string) string {
	if hostPort == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(hostPort); err == nil {
		return h
	}
	return strings.Trim(hostPort, "[]")
}
