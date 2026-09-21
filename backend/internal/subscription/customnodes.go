package subscription

import (
	"crypto/rand"
	"encoding/hex"
	"fluxor/internal/config"
	"fluxor/internal/configgen"
	"fluxor/internal/nodespec"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// normalizeCustomNodes 校验并归一化自定义模式的节点列表。
//
// 校验分三层，缺一层都可能产出内核加载不了的配置（实测内核会拒绝整份配置）：
//  1. 节点名：非空 / 长度 / 无控制字符 / 列表内不重名（`proxy A is the duplicate name`）；
//  2. 名字占用：不得与模板里的代理组或内置目标（DIRECT / REJECT / PASS）同名
//     （`proxy group X: the duplicate name`）——占用集合直接取该模式规则环境的
//     目标集合，与自定义规则的可选目标同源，模板一改这里自动跟随；
//  3. 协议字段：由 nodespec 负责（类型转换、必填、组合要求、未知键、下拉选项），
//     并按「只存与默认值不同的字段」落库。
//
// 新节点补 ID、已有节点沿用旧 ID：前端按 ID 编辑与删除单条节点。
func normalizeCustomNodes(nodes []config.CustomNode) ([]config.CustomNode, error) {
	if len(nodes) == 0 {
		return nil, nil
	}
	if err := config.ValidateCustomNodes(nodes); err != nil {
		return nil, err
	}

	reserved, err := nodeNameReservedSet()
	if err != nil {
		return nil, err
	}

	out := make([]config.CustomNode, 0, len(nodes))
	for i := range nodes {
		node := nodes[i]
		if _, taken := reserved[node.Name]; taken {
			return nil, fmt.Errorf("第 %d 个节点（%s）与模板中的代理组/内置目标同名，内核会拒绝加载配置，请改名", i+1, node.Name)
		}
		node.ID = strings.TrimSpace(node.ID)
		if node.ID == "" {
			node.ID = newCustomNodeID()
		}
		normalized, err := nodespec.Normalize(node)
		if err != nil {
			return nil, fmt.Errorf("第 %d 个节点（%s）: %w", i+1, node.Name, err)
		}
		out = append(out, normalized)
	}
	return out, nil
}

// nodeNameReservedSet 返回自定义模式模板里已被占用的名字（代理组 + 内置目标）。
//
// 自定义模式固定使用标准规则集，因此直接复用该档位的规则环境：它与界面上
// 「自定义规则」的目标下拉、以及生成配置时的校验集合是同一份来源。
func nodeNameReservedSet() (map[string]struct{}, error) {
	env, err := configgen.MergeRuleSetEnv(config.RuleGroupBase)
	if err != nil {
		return nil, fmt.Errorf("读取标准规则集失败: %w", err)
	}
	return env.Targets, nil
}

// newCustomNodeID 生成节点标识（前端编辑/删除单个节点使用）。
func newCustomNodeID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(buf)
}
