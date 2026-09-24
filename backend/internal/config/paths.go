package config

var (
	// SocketPath Unix Domain Socket 监听路径；为空表示禁用 Unix Socket 监听。
	SocketPath string
	// BaseURL 所有 HTTP 路由的统一前缀（例如 /app/Fluxor）；为 / 时归一化为空串。
	BaseURL string
	// FluxorDataDir 统一的运行数据目录：fluxor.json 与 fluxor.log 一律生成在该目录下
	// （由 SetDataDir 派生，两者不再各自配置）。
	FluxorDataDir string
	// FluxorPidFile Fluxor 自身进程的 PID 文件路径（默认 FluxorDataDir/fluxor.pid；
	// openwrt 模式固定在 /var/run/fluxor.pid，不随数据目录变化）。
	FluxorPidFile string
	// FluxorBinDir Fluxor 二进制所在目录，自更新时用于备份与替换。
	FluxorBinDir string
	// CorePidFile 内核进程 PID 文件路径（默认 FluxorDataDir/core.pid；openwrt 模式
	// 固定在 /var/run/core.pid），用于判断内核是否在运行。
	CorePidFile string
	// CoreBin Mihomo 内核可执行文件路径。
	CoreBin string
	// CoreSocket 内核暴露的 Unix Socket 路径，Fluxor 经此转发全部内核 API。
	CoreSocket string
	// MetaDir MetaCubeXD 外部面板静态文件目录。
	MetaDir string
	// ZashDir Zashboard 外部面板静态文件目录。
	ZashDir string
	// FluxorConfigFile Fluxor 的 JSON 配置与持久化状态文件（FluxorDataDir/fluxor.json）。
	FluxorConfigFile string
	// ConfigTarget 生成给内核使用的 config.yaml 目标路径。
	ConfigTarget string
	// FluxorLogFile Fluxor 后端全部日志的落盘文件（FluxorDataDir/fluxor.log），
	// 由 logx 写入，见 internal/logx/doc.go。
	FluxorLogFile string
	// CoreWorkDir 内核工作目录，其下 proxies/ 存放各订阅的节点文件。
	CoreWorkDir string
	// TcpAddr 可选的 TCP 监听地址；与 SocketPath 至少有一个非空。
	TcpAddr string
	// OriginalBaseURL 未经归一化处理的原始 BaseURL，注入前端作为 window.BASE_URL。
	OriginalBaseURL string
)
