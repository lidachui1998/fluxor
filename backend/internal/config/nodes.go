package config

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// MaxCustomNodeNameLength 自定义节点名长度上限（按字符计，含 emoji）。
//
// 比订阅名（32）宽松：节点名会被写进 config.yaml 的 proxies[].name 与规则目标，
// 机场与用户习惯用「🇭🇰 香港 01 · 优化」这类较长的名字。
const MaxCustomNodeNameLength = 64

var (
	// ErrCustomNodeNameEmpty 节点名为空。
	ErrCustomNodeNameEmpty = errors.New("节点名不能为空")
	// ErrCustomNodeNameTooLong 节点名超长。
	ErrCustomNodeNameTooLong = fmt.Errorf("节点名过长（最多 %d 个字符）", MaxCustomNodeNameLength)
	// ErrCustomNodeNameInvalid 节点名含不可用字符（控制字符、换行等）。
	ErrCustomNodeNameInvalid = errors.New("节点名包含不可用的字符")
)

// ValidateCustomNodeName 校验自定义模式的节点名。
//
// 与订阅名（ValidateSubscriptionName）的差异：节点名不参与文件路径与映射键，
// 只作为 proxies[].name 与规则目标，因此允许空格、标点与中文，仅拒绝
//   - 空名（内核要求 name 必填）
//   - 控制字符与换行（会把配置写成难以排查的怪样子）
//   - 超过 MaxCustomNodeNameLength
//
// 与模板代理组名、内置目标（DIRECT/REJECT/PASS）同名会被内核拒绝
// （实测 `proxy group X: the duplicate name`），该校验需要模板信息，故不在此处，
// 由 configgen 的保留名集合负责（见 subscription.normalizeCustomNodes）。
func ValidateCustomNodeName(name string) error {
	if strings.TrimSpace(name) == "" {
		return ErrCustomNodeNameEmpty
	}
	if len([]rune(name)) > MaxCustomNodeNameLength {
		return ErrCustomNodeNameTooLong
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: %q", ErrCustomNodeNameInvalid, r)
		}
	}
	return nil
}

// ValidateCustomNodes 校验一组节点：逐个校验名称，并检查重名。
//
// 重名必须拦截：内核遇到两个同名 proxy 会拒绝加载整份配置
// （实测 `proxy A is the duplicate name`），此时面板与内核一起不可用。
func ValidateCustomNodes(nodes []CustomNode) error {
	seen := make(map[string]int, len(nodes))
	for i, node := range nodes {
		if err := ValidateCustomNodeName(node.Name); err != nil {
			return fmt.Errorf("第 %d 个节点: %w", i+1, err)
		}
		if prev, dup := seen[node.Name]; dup {
			return fmt.Errorf("第 %d 个节点与第 %d 个重名: %s", i+1, prev+1, node.Name)
		}
		seen[node.Name] = i
	}
	return nil
}

// FindCustomNode 按 id 查找节点，返回其下标（未找到返回 -1）。
func FindCustomNode(nodes []CustomNode, id string) int {
	for i := range nodes {
		if nodes[i].ID == id {
			return i
		}
	}
	return -1
}
