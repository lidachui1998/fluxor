package appupdate

import "testing"

// compareVersions 决定「要不要提示有新版本」，判错的两种后果都是静默的：
// 漏报（用户永远等不到更新）或误报（天天提示已是最新版本时还让更新）。
//
// 旧实现按 "." 切开逐个 strconv.Atoi 且忽略错误，于是 "0-rc1" 这类段被当作 0：
// compareVersions("1.1.0", "1.1.0-rc1") == 0 —— 跑预发布版的用户收不到正式版提示。
func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		// 常规比较
		{"1.0.0", "1.0.0", 0},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "1.0.1", -1},
		{"2.0.0", "1.9.9", 1},
		{"1.10.0", "1.9.0", 1}, // 按数值而非字典序
		// 段数不等：缺失段补 0
		{"1.2", "1.2.0", 0},
		{"1.2.1", "1.2", 1},
		{"1", "1.0.0", 0},
		// v 前缀（GitHub tag 与 make V= 的写法未必一致）
		{"v1.0.1", "1.0.0", 1},
		{"V1.0.1", "v1.0.0", 1},
		// 构建元数据不参与比较
		{"1.0.0+build5", "1.0.0", 0},
		// ↓ 回归点：预发布版本小于同版本的正式版
		{"1.1.0", "1.1.0-rc1", 1},
		{"1.1.0-rc1", "1.1.0", -1},
		{"1.1.0-rc1", "1.1.0-rc1", 0},
		{"1.1.0-rc2", "1.1.0-rc1", 1},
		// 预发布之间：数字标识符按数值比，数字 < 非数字
		{"1.1.0-beta.10", "1.1.0-beta.2", 1},
		{"1.1.0-beta", "1.1.0-beta.1", -1}, // 标识符少的一方更小
		{"1.1.0-alpha", "1.1.0-beta", -1},
		{"1.1.0-1", "1.1.0-alpha", -1}, // 数字 < 非数字
		// 预发布的比较优先于核心段：核心段更大就更大
		{"1.2.0-rc1", "1.1.0", 1},
		// 脏输入不应导致「静默相等」
		{"1.1.0-rc1", "1.1.0-rc1~670ab34", -1}, // ~ 后缀视为预发布标识的延续
		{"", "1.0.0", -1},
		{"1.0.0", "", 1},
		{"", "", 0},
		{"abc", "1.0.0", -1}, // 无数字段按 0
	}
	for _, tc := range cases {
		t.Run(tc.a+" vs "+tc.b, func(t *testing.T) {
			if got := compareVersions(tc.a, tc.b); got != tc.want {
				t.Fatalf("compareVersions(%q, %q) = %d，期望 %d", tc.a, tc.b, got, tc.want)
			}
			// 反对称性：反过来比应当取反（相等时为 0）
			rev := compareVersions(tc.b, tc.a)
			if rev != -tc.want {
				t.Fatalf("反对称性被破坏：compareVersions(%q,%q)=%d 而 compareVersions(%q,%q)=%d",
					tc.a, tc.b, tc.want, tc.b, tc.a, rev)
			}
		})
	}
}

// 「是否有更新」的实际判定必须能识别预发布 → 正式版。
func TestHasUpdateDetectsPrereleaseToStable(t *testing.T) {
	// 用户跑 1.1.0-rc1，远端发布 1.1.0
	if compareVersions("1.1.0", "1.1.0-rc1") <= 0 {
		t.Fatal("预发布版用户必须能收到同版本的正式版更新提示")
	}
	// 用户已在最新正式版：不应提示
	if compareVersions("1.1.0", "1.1.0") > 0 {
		t.Fatal("同为正式版不应提示有更新")
	}
	// 反向：跑正式版时不应把 rc 当成「更新」
	if compareVersions("1.1.0-rc1", "1.1.0") > 0 {
		t.Fatal("正式版用户不应被降级提示到预发布版")
	}
}
