package appupdate

// Package appupdate 处理 Fluxor 自身与 Mihomo 内核的版本检查及自更新。
//
// 文件划分：
//   - github.go      GitHub Release / Tag 查询与语义化版本比较
//   - cache.go       版本信息内存缓存（TTL 10 分钟）
//   - coreversion.go 内核本地版本读取（经 Unix Socket）与远程版本比对
//   - selfupdate.go  Fluxor 自更新：优先拉取 fluxor-<arch>.tar.gz 压缩包（解压提取二进制），
//     次选 fluxor-<arch> 裸二进制；下载只走「自身代理」与「直连」两条链路，各重试 2 次，
//     全部失败即提示检查代理或网络连接；成功后备份旧二进制、替换并重启
//   - handler.go     /check-update HTTP 接口
