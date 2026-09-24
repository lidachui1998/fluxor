package config

// Package config 集中定义 Fluxor 的全局运行配置与持久化状态。
//
// 该包是整个后端的“叶子”依赖：只做路径/模式解析、订阅配置的读写与全局状态
// 的持有，绝不反向依赖 core、tproxy、subscription 等上层包，从而避免循环依赖。
//
// 主要职责：
//   - paths.go     运行路径（Socket、PID、内核二进制、配置目标等）的默认值
//   - modes.go     按运行模式（fnos / openwrt）套用默认路径
//   - name.go      订阅名校验与节点文件名清洗（防路径穿越）
//   - model.go     SubscribeConfig / Subscription 数据结构定义
//   - state.go     进程内共享的当前配置与读写锁
//   - load.go      配置的加载、默认值补齐与持久化；
//   - store.go     泛型 Store[T]：一个 JSON 文件 = 一把独立锁 + 一份内存态 + 一个
//                  写入者；原子写盘与「损坏则拒写」
//   - storemodels.go 各文件的磁盘结构（Settings / RulesFile / TunnelsFile /
//                  MetaFile / TproxyFile）
//   - stores.go    各文件唯一的读写入口、视图组装（Current）、孤儿回收与改名搬迁
//   - migrate.go   旧单文件 fluxor.json → 多文件的一次性拆分（幂等、可回滚）
//
// 注：main.go 中的环境变量覆盖直接调用 os.Getenv，本包不再提供读取工具。
// 锁分两层：每个 Store 一把文件锁（只保护该文件的「读—改—写」），config.Mu 保护
// 内存视图 Current。二者不可嵌套：Store 的写入口内部会重组装 Current，因此不得在
// 持有 config.Mu 时调用它们。
