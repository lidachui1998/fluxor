// Package logx 是 Fluxor 后端唯一的日志出口（叶子包，不依赖其它 internal 包）。
//
// 为什么要有这个包：日志原本分散在三处——部分走 stdlib `log`（→ stderr）、部分走
// `fmt.Printf`（→ stdout）、少量错误由 core.CoreLogger 直接写进当时的日志文件（info.log，现已随本包一并移除）。三者各自的
// 落点取决于**启动方式**（fnOS 启动脚本把 stdout/stderr 重定向到日志文件；openwrt 的
// systemd/procd 则进 journal），导致「同一份日志在不同平台落在不同文件、内容还不一样」，
// 甚至出现「面板显示有日志文件，实际里面只有几行错误」的误导。
//
// 现在的约定（改动日志时必须遵守）：
//  1. **唯一出口**：全部日志经本包输出，禁止再直接使用 `fmt.Print*` / `log.Print*` /
//     自建 `*log.Logger`。启动脚本不再需要（也不应）把 stdout/stderr 重定向到日志文件，
//     否则日志会被拆成互不相关的两份。
//  2. **唯一文件**：写入运行数据目录下的 fluxor.log（`config.FluxorLogFile`），
//     由 main.go 在解析完路径后调用 Setup 打开；同时镜像到 stderr，前台启动仍可见。
//  3. **记录格式**：`2006-01-02 15:04:05.000 LEVEL [MODULE] message`
//     ——本地时间戳（毫秒）、等级、大功能模块标记、正文。
//  4. **正文一律英文**：本文件的调用方消息全部为英文（含 CLI 用法提示），
//     变量用 %q/%d/%v 或 key=value 内联；代码注释仍保持中文。
//     注意 logx 在**无参数**时不做格式化（避免正文里的字面量 % 被 fmt 破坏），
//     因此正文里需要百分号时必须经参数传入（`"progress %s", "100%"`）：
//     直接写 `"progress 100%"` 会被 go vet 判为「format % is missing verb」。
//  5. **模块标记只用本包常量**：ModuleMain / ModuleCore / ModuleSub / …，
//     粒度对齐后端功能域（不是每个文件一个标记），这样 `grep '\[CORE\]'` 就能取到
//     内核生命周期的全部记录。新增功能域时在本包登记常量。
//  6. **等级语义**：
//     - DEBUG 内部步骤、正常路径细节（缓存命中、跳过已有项、定时器启停）；
//     - INFO  有意义的状态变化（启动/停止成功、订阅更新完成、配置生成与重载）；
//     - WARN  可继续运行但偏离预期（无效项被跳过、回退临时内核、清理失败）；
//     - ERROR 某次操作确实失败（生成/启动/写入/重载失败、防火墙应用失败）。
//     等级由环境变量 FLUXOR_LOG_LEVEL 控制（缺省 info）。
//
// 文件划分：
//   - logx.go 等级与模块常量、Setup/Close、Debug/Info/Warn/Error 与格式化
package logx
