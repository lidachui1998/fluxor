package config

import "fmt"

// 端口的取值约束。与前端「订阅中心」页的校验保持同一套规则（1025-65535，非零端口
// 互不重复），但后端是真正的边界：前端校验只是体验，绕过它（直接调接口、旧版前端、
// 脚本）不应该能写进一份内核拒绝加载或语义错乱的配置。
const (
	// MinUserPort 非特权端口下界：低于 1025 需要 root 才能绑定，而面板与内核都不以
	// root 运行（TProxy 规则由 nft 承担），因此一律拒绝。
	MinUserPort = 1025
	// MaxUserPort 端口上界。
	MaxUserPort = 65535
)

// ValidateLocalPorts 校验面板自己持有的三个端口。
//
// 语义差异是刻意的，与前端的判定一致：
//   - ProxyPort / PanelPort 为必填（内核必须有一个混合代理端口与一个外部控制端口，
//     取 0 会让内核随机选端口或直接不监听，用户无法预期）；
//   - TproxyPort 允许 0，表示「不启用透明代理」（开关路径也会拒绝在端口为 0 时启用）。
//
// 非零端口之间不得重复：同一个端口被两个角色监听时后绑定的那个必然失败，而失败发生在
// 内核启动阶段，表现为「内核起不来」而不是「某个端口没生效」，排查成本很高，因此在写盘前拦住。
func ValidateLocalPorts(cfg SubscribeConfig) error {
	if err := validateRequiredPort("代理端口", cfg.ProxyPort); err != nil {
		return err
	}
	if err := validateRequiredPort("面板端口", cfg.PanelPort); err != nil {
		return err
	}
	if cfg.TproxyPort != 0 {
		if err := validateRequiredPort("TPROXY 端口", cfg.TproxyPort); err != nil {
			return err
		}
	}

	seen := map[int]string{}
	for _, p := range []struct {
		name  string
		value int
	}{
		{"代理端口", cfg.ProxyPort},
		{"面板端口", cfg.PanelPort},
		{"TPROXY 端口", cfg.TproxyPort},
	} {
		if p.value == 0 {
			continue
		}
		if prev, ok := seen[p.value]; ok {
			return fmt.Errorf("%s与%s不能相同（都是 %d）", prev, p.name, p.value)
		}
		seen[p.value] = p.name
	}
	return nil
}

// ValidateOptionalPort 校验单个可留空的端口（用于 /configs PATCH 这类只改一个字段的入口）。
//
// 0 视为「不设置」，直接放行——内核的部分端口字段确实以 0 表示禁用。
func ValidateOptionalPort(name string, port int) error {
	if port == 0 {
		return nil
	}
	return validateRequiredPort(name, port)
}

func validateRequiredPort(name string, port int) error {
	if port < MinUserPort || port > MaxUserPort {
		return fmt.Errorf("%s %d 超出允许范围（%d-%d）", name, port, MinUserPort, MaxUserPort)
	}
	return nil
}

// NormalizeRuleGroup 归一化规则集档位，并回报是否可接受。
//
// merge 模式下档位是必填且必须合法（生成链路按档位取代理组与规则集，未知档位会拒绝生成）；
// 其余模式下该控件在界面上不可见，用户提交旧值/空值属正常，统一回落 base 而不是报错——
// 报错等于把一个用户看不到的字段变成「保存永久失败」。
func NormalizeRuleGroup(ruleGroup, mode string) (string, error) {
	if IsValidRuleGroup(ruleGroup) {
		return ruleGroup, nil
	}
	if mode == ModeMerge {
		return "", fmt.Errorf("规则集档位无效：%q（应为 %s 或 %s）", ruleGroup, RuleGroupBase, RuleGroupFull)
	}
	return RuleGroupBase, nil
}

// NormalizeMode 归一化运行模式：空值回落 merge（与旧行为一致），未知取值直接拒绝。
//
// 不能像旧实现那样把未知模式当成 merge 继续走并**原样落库**：落一个不存在的模式进
// settings.json 之后，模式判断（生成链路按 mode 分派、定时器按 mode 启停）会各自
// 走进「既不是 switch 也不是 custom」的分支，行为无从预期。
func NormalizeMode(mode string) (string, error) {
	if mode == "" {
		return ModeMerge, nil
	}
	if IsValidMode(mode) {
		return mode, nil
	}
	return "", fmt.Errorf("无效的运行模式：%q（应为 %s / %s / %s）", mode, ModeMerge, ModeSwitch, ModeCustom)
}
