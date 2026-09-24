package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"fluxor/internal/logx"
)

// Store[T] 一份 JSON 文件 = 一把独立锁 + 一份内存态 + 一个写入者。
//
// 为什么要有它：此前全部持久化状态挤在 fluxor.json 里，由 config 与 tproxy 两个包
// 分别「读整文件—改—写整文件」，共用一把全局锁。由此产生四个问题（详见 AGENTS 3.5）：
//   - 写放大：改一条规则要重写整份文件（含所有订阅的机场元数据与预填模板）；
//   - 全量阻塞：一把锁串行化两个功能域，定时更新期间用户改绕过列表要排队；
//   - 损坏半径 100%：解析失败时以空 map 为基底继续写，会把**别人的字段**一并清空；
//   - 非原子写盘：裸 os.WriteFile 崩溃/断电会留下半截文件。
//
// 本类型把「一个文件只有一个写入者」变成实现约束：锁按文件粒度隔离，序列化的是带类型的
// 结构体（不可能写到别人的字段），写盘走「临时文件 + fsync + rename」，解析失败则备份
// 原文件并拒绝写入——损坏的影响面仅限该文件自身，且原始内容始终可恢复。
//
// 用法（每个数据类别一个包级单例）：
//
//	var settingsStore = NewStore[Settings]("settings.json", &FluxorSettingsFile, ...)
//
// 约束：Update 持锁期间**不得**调用其它 Store（否则会出现 A→B 的锁序）；需要跨文件
// 一致性时，先让各 Update 依次返回，再调用 assembleCurrent 之类的一致性装配函数。
type Store[T any] struct {
	// Name 文件名（仅用于日志与报错文案）。
	Name string
	// Path 指向路径变量：Store 构造时数据目录可能尚未确定（SetDataDir 晚于包初始化），
	// 因此持有指针、每次读盘写盘时再解析。
	Path *string
	// Init 在读盘**之前**填入默认值：JSON 里缺失的键会保留这里的取值（键存在时被覆盖）。
	Init func(*T)
	// Normalize 在读盘**之后**修正「反序列化无法自行补齐」的部分：JSON 里写成 null
	// 的切片/map、以及空字符串回落默认值等。
	//
	// 注意职责边界：标量默认值只能放在 Init（读盘前），因为 Normalize 无法区分
	// 「键缺失」与「用户显式写了零值」——例如端口 0 表示禁用，写成 0 必须保留。
	Normalize func(*T)

	mu      sync.Mutex
	value   T
	corrupt bool
}

// NewStore 构造一个 Store。init 可为 nil（结构体零值即默认值）。
func NewStore[T any](name string, path *string, init func(*T)) *Store[T] {
	return &Store[T]{Name: name, Path: path, Init: init}
}

// FilePath 返回当前解析出的文件路径（每次解析：数据目录可能在启动后被 SetDataDir 改写）。
func (s *Store[T]) FilePath() string {
	if s.Path == nil {
		return ""
	}
	return *s.Path
}

// Load 从磁盘载入内存态；文件不存在时保持默认值。
//
// 解析失败时：把原始内容另存为 `<file>.corrupt-<时间戳>`、内存态回落默认值、
// **拒绝后续写入**，并返回错误。这里刻意选择「不静默以空值继续」，因为静默继续
// 意味着下一次写入会把用户手工编辑但写坏的内容彻底覆盖掉。
func (s *Store[T]) Load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *Store[T]) load() error {
	s.corrupt = false
	s.value = *new(T)
	if s.Init != nil {
		s.Init(&s.value)
	}

	path := s.FilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		// 读盘失败（EACCES / EIO / 目录不存在等）同样必须拒绝后续写入。
		//
		// 此时内存态已在上面回落到默认值，而磁盘上的原文件仍然完好。若继续放行写入，
		// 下一次保存就会把「默认值」覆盖到用户的真实配置上——这是最隐蔽的一种数据丢失：
		// 没有报错、没有备份，只是某次重启后配置变成了初始状态。
		s.corrupt = true
		logx.Error(logx.ModuleConfig, "failed to read %s, loaded defaults and disabled writes (fix permissions or restore the file, then restart): %v",
			s.Name, err)
		return fmt.Errorf("读取 %s 失败（已停止写入以免用默认值覆盖磁盘内容）: %w", s.Name, err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}

	if err := json.Unmarshal(data, &s.value); err != nil {
		backup := fmt.Sprintf("%s.corrupt-%s", path, time.Now().Format("20060102-150405"))
		if werr := os.WriteFile(backup, data, 0644); werr != nil {
			logx.Error(logx.ModuleConfig, "failed to back up corrupt %s to %s: %v", s.Name, backup, werr)
		}
		logx.Error(logx.ModuleConfig, "%s is not valid JSON, loaded defaults and disabled writes (backup: %s): %v",
			s.Name, backup, err)
		if s.Init != nil {
			s.Init(&s.value)
		}
		if s.Normalize != nil {
			s.Normalize(&s.value)
		}
		s.corrupt = true
		return fmt.Errorf("%s 内容损坏（已备份到 %s），修复或删除该文件后重启面板即可恢复写入: %w",
			s.Name, backup, err)
	}

	if s.Normalize != nil {
		s.Normalize(&s.value)
	}
	return nil
}

// Damaged 报告该 store 当前是否处于「内容不可信」状态：内容损坏（解析失败）或读盘失败。
//
// 两种情况都会让内存态回落到默认值，而磁盘上的原始内容并未被清空，因此**都不能**
// 把内存态当作真相使用——尤其是 GCResources 这类「按内存态反推磁盘该删什么」的逻辑，
// 一旦以默认值（订阅表为空）为基底，就会把别的文件里的数据全部判成孤儿。
//
// 与 Update 的拒绝写入是同一判据的两个面：内部拒绝写，外部（跨文件维护逻辑）据此
// 决定要不要动手。
func (s *Store[T]) Damaged() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.corrupt
}

// View 在锁内以只读方式访问内存态。
//
// 回调**不得**保留指针、不得修改内容、不得在回调内调用其它 Store（锁序约束见类型注释）；
// 需要把切片/map 带出回调时必须自行拷贝。
func (s *Store[T]) View(fn func(*T)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.value)
}

// Update 在锁内修改内存态并原子落盘。
//
// mutate 返回错误时不落盘（但已发生的内存改动不会回滚，与旧实现一致）；
// 文件处于「损坏」状态时直接拒绝写入。
func (s *Store[T]) Update(mutate func(*T) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.corrupt {
		return fmt.Errorf("%s 内容损坏：为免覆盖现场已停止写入，请修复或删除该文件后重启面板", s.Name)
	}
	if mutate != nil {
		if err := mutate(&s.value); err != nil {
			return err
		}
	}
	return s.flush()
}

// flush 序列化并原子写盘（调用方须已持锁）。
func (s *Store[T]) flush() error {
	out, err := json.MarshalIndent(s.value, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 %s 失败: %w", s.Name, err)
	}
	out = append(out, '\n')
	path := s.FilePath()
	if err := writeFileAtomic(path, out); err != nil {
		return fmt.Errorf("写入 %s 失败: %w", s.Name, err)
	}
	logx.Debug(logx.ModuleConfig, "%s written: path=%s bytes=%d", s.Name, path, len(out))
	return nil
}

// writeFileAtomic 以「临时文件 + fsync + rename + fsync(父目录)」写入，
// 避免崩溃/断电留下半截或「名字对、内容空」的文件。
//
// 临时文件与目标同目录（保证 rename 不跨设备），并显式 chmod 到 0644：CreateTemp
// 默认 0600，直接 rename 会让配置文件对同组用户不可读（fnOS 下启动脚本与面板可能不同用户）。
//
// 最后一步（同步父目录）不是可选的：rename 改的是**父目录的目录项**，而 fsync(tmp)
// 只保证那个文件的数据落盘。少了目录同步，断电后 rename 可能整个丢失，更糟的是在
// ext4 这类延迟分配的日志文件系统上，目录项已经更新而新文件的数据块还没落盘——
// 重启后得到一个长度正确、内容全零的配置文件（对配置来说就是「面板起来是默认值」）。
// 这是 SQLite / PostgreSQL 那套 fsync(file) → rename → fsync(dir) 的标准做法。
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		// 成功路径下 tmpName 已被 rename 掉，Remove 只会在失败路径生效
		_ = os.Remove(tmpName)
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return syncDir(dir)
}

// syncDir 把目录自身的元数据刷盘，让其中的 rename/create/remove 变更跨断电存活。
//
// 失败只记日志、不返回错误：目录 fsync 在部分平台/文件系统上本就不支持（Windows 上
// 对目录调用 Sync 会直接报错），而配置文件此刻已经写好了。因为一个"加固步骤"失败就
// 把整次保存判为失败，会让面板在那些平台上完全无法保存——得不偿失。记一条 WARN
// 是为了让"这台机器上持久性没有保障"这件事在日志里可见。
func syncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		logx.Warn(logx.ModuleConfig, "failed to open %s for directory sync, rename durability is not guaranteed: %v", dir, err)
		return nil
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		logx.Warn(logx.ModuleConfig, "failed to sync directory %s, rename durability is not guaranteed: %v", dir, err)
	}
	return nil
}
