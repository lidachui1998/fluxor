package nodespec

// 本文件收录字段构造函数：让各协议的字段表能以接近声明式表格的形式书写，
// 只表达「键 / 展示名 / 类型 / 默认值 / 可选值 / 是否必填 / 属于哪个分区」这些事实。

// str 单行文本字段。
func str(key, label string) Field {
	return Field{Key: key, Label: label, Kind: KindString}
}

// strDef 带默认值的单行文本字段。
func strDef(key, label, def string) Field {
	return Field{Key: key, Label: label, Kind: KindString, Default: def}
}

// secret 敏感单行文本字段（界面默认掩码）。
func secret(key, label string) Field {
	return Field{Key: key, Label: label, Kind: KindString, Secret: true}
}

// text 多行文本字段（PEM 证书、密钥等）。
func text(key, label string) Field {
	return Field{Key: key, Label: label, Kind: KindText}
}

// secretText 敏感多行文本字段（私钥等）。
func secretText(key, label string) Field {
	return Field{Key: key, Label: label, Kind: KindText, Secret: true}
}

// flag 布尔开关（默认 false，与内核默认一致）。
func flag(key, label string) Field {
	return Field{Key: key, Label: label, Kind: KindBool}
}

// num 整数字段。
func num(key, label string) Field {
	return Field{Key: key, Label: label, Kind: KindInt}
}

// numDef 带默认值的整数字段。
func numDef(key, label string, def int) Field {
	return Field{Key: key, Label: label, Kind: KindInt, Default: def}
}

// sel 下拉字段：def 为空字符串表示「不预选」，界面显示空选项（= 用内核默认）。
func sel(key, label, def string, options ...string) Field {
	return Field{Key: key, Label: label, Kind: KindSelect, Default: def, Options: options}
}

// list 列表字段（界面按逗号/换行分隔输入）。
func list(key, label string) Field {
	return Field{Key: key, Label: label, Kind: KindList}
}

// pairs 键值对字段（界面按「键: 值」每行一条输入）。
func pairs(key, label string) Field {
	return Field{Key: key, Label: label, Kind: KindMap}
}

// group 嵌套选项块：children 为块内字段，when 为空表示无条件渲染。
func group(key, label string, when *VisibleWhen, children ...Field) Field {
	return Field{Key: key, Label: label, Kind: KindGroup, Children: children, VisibleWhen: when}
}

// on 构造条件显示：同层 key 的取值命中 values 时才渲染该字段。
func on(key string, values ...string) *VisibleWhen {
	return &VisibleWhen{Key: key, Values: values}
}

// req 标记必填。
func req(f Field) Field {
	f.Required = true
	return f
}

// always 标记「始终下发」：内核解码器要求该键必须存在（零值也要写）。
func always(f Field) Field {
	f.Always = true
	return f
}

// basicSection 基础分区：默认展开。
func basicSection(fields ...Field) []Field {
	return section(SectionBasic, fields)
}

// transportSection 传输层分区：含 network 与各传输选项块（默认折叠）。
func transportSection(fields ...Field) []Field {
	return section(SectionTransport, fields)
}

// tlsSection 分区：TLS 开关之外的全部 TLS 配置（默认折叠）。
func tlsSection(fields ...Field) []Field {
	return section(SectionTLS, fields)
}

// advancedSection 高级分区：默认折叠。
func advancedSection(fields ...Field) []Field {
	return section(SectionAdvanced, fields)
}

// section 给一组字段打上分区标记。
//
// 嵌套块的子字段不打分区（由所属块决定渲染位置），因此调用方只在顶层字段上
// 使用 basicSection / transportSection / tlsSection / advancedSection。
func section(name string, fields []Field) []Field {
	out := make([]Field, 0, len(fields))
	for _, f := range fields {
		f.Section = name
		out = append(out, f)
	}
	return out
}

// concat 按顺序拼接多段字段（basic → transport → tls → advanced 即最终界面顺序）。
func concat(parts ...[]Field) []Field {
	out := make([]Field, 0, 16)
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

// basicCommon 返回通用高级项（BasicOption 除 dialer-proxy 外的内容，作为高级区）。
func basicCommon() []Field {
	return advancedSection(basicAdvanced...)
}
