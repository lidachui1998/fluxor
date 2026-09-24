package dashapi

import (
	"encoding/json"
	"fmt"
	"math"

	"fluxor/internal/config"
	"fluxor/internal/logx"
)

// 本文件处理 /configs PATCH 里的 tproxy-port：它是唯一一个「面板自己也在用」的内核字段。
//
// 为什么不能只把它当成一次普通的内核配置转发：
//   - 内核按它监听透明代理端口，nft 规则按它把流量重定向过来，两者必须是同一个数；
//   - 面板的「保存并应用」会按 settings.json 里的值重新生成 config.yaml，因此
//     settings.json 才是唯一真相——只改内核会被下一次生成悄悄改回去；
//   - 「配置」页的 TPROXY 端口输入框与「订阅中心」页的 tproxy_port 字段是同一个设置的
//     两个入口，必须写同一处，否则两个页面会长期显示不同的值。

// tproxyPortFromPatch 从 PATCH /configs 的请求体里取出 tproxy-port。
//
// changed=false 表示本次修改与端口无关，调用方不应触碰 settings.json 与防火墙规则。
// 请求体不是 JSON 对象时同样返回 changed=false：那属于内核会明确拒绝的非法请求，
// 由内核去报错，面板不在这里抢先下结论。
func tproxyPortFromPatch(body []byte) (port int, changed bool, err error) {
	if len(body) == 0 {
		return 0, false, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return 0, false, nil
	}
	raw, ok := fields["tproxy-port"]
	if !ok {
		return 0, false, nil
	}

	var num float64
	if err := json.Unmarshal(raw, &num); err != nil {
		return 0, false, fmt.Errorf("TPROXY 端口必须是数字")
	}
	// 先做浮点范围判定再转 int：Go 对超出 int 范围的浮点转整数是未定义结果，
	// 而 JSON 允许 1e18 这种取值，直接用 int(num) 会得到一个无意义的端口号。
	if num != math.Trunc(num) {
		return 0, false, fmt.Errorf("TPROXY 端口必须是整数")
	}
	if num < 0 || num > math.MaxInt32 {
		return 0, false, fmt.Errorf("TPROXY 端口 %v 超出允许范围", num)
	}
	p := int(num)
	if err := config.ValidateOptionalPort("TPROXY 端口", p); err != nil {
		return 0, false, err
	}
	return p, true, nil
}

// persistTproxyPort 把端口写进 settings.json，返回修改前的取值与「是否真的变了」。
//
// **changed=false 时调用方必须什么都不做**（不回滚、不重建规则）。前端的端口表单是
// 「一失焦就整体提交」的，用户点一下输入框再点到别处（移动端尤其容易）就会带着同一个
// 端口值打一次 PATCH；若按「请求里出现了 tproxy-port」判定为变更，就会白白把防火墙
// 规则拆掉重建一次——在 TProxy 正在接管时那是一次不必要的网络瞬断。
//
// 传 config.Current 给 SaveSettings 是安全的：SaveSettings 只取设置类字段（全局标量 +
// 订阅注册表 + 手工节点），规则 / 隧道 / 元数据不在它的作用域内，因此这次写入不会
// 波及它们——这正是拆分存储带来的好处。
func persistTproxyPort(port int) (prev int, changed bool, err error) {
	config.Mu.RLock()
	view := config.Current
	config.Mu.RUnlock()

	prev = view.TproxyPort
	if prev == port {
		return prev, false, nil
	}
	view.TproxyPort = port
	if err := config.SaveSettings(view); err != nil {
		return prev, false, err
	}
	return prev, true, nil
}

// rollbackTproxyPort 把端口改回 prev（best effort）。
//
// 用在「内核拒绝了这次 PATCH」之后：库里必须回到与内核一致的值，否则下一次生成会以
// 「库里是对的」为由把正在运行的内核改坏。回滚失败只记日志——此时面板已经处于不一致
// 状态，能做的只是把事实写进日志让用户可见。
func rollbackTproxyPort(prev int) {
	config.Mu.RLock()
	view := config.Current
	config.Mu.RUnlock()

	if view.TproxyPort == prev {
		return
	}
	view.TproxyPort = prev
	if err := config.SaveSettings(view); err != nil {
		logx.Error(logx.ModuleTproxy, "failed to roll back tproxy port to %d after core rejected the change: %v", prev, err)
		return
	}
	logx.Warn(logx.ModuleTproxy, "tproxy port rolled back to %d because the core rejected the change", prev)
}
