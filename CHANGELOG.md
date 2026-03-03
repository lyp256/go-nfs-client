# 更新日志

## 2026/03/03

### 新功能
- **nfs.go/target.go**: 新增 `FSStat()` 方法，支持获取 NFS 文件系统空间使用情况（总大小、已用空间、可用空间、文件槽位统计）

## 2026/02/28

### 安全修复
- **tcp.go**: 在 `recv()` 中添加 `MaxMessageSize = 16MB` 限制，防止恶意服务器发送超大消息导致内存耗尽
- **xdr/decode.go**: 在 `ReadOpaque()` 中添加 `MaxOpaqueSize = 1MB` 限制，防止内存耗尽攻击

### Bug 修复
- **file.go**: 修复 `Seek()` 整数溢出风险 - 为 `io.SeekCurrent` 添加边界检查，防止负数偏移
- **file.go**: 修复 `Write()` 可能死循环 - 添加 `Count == 0` 检查，检测服务器返回零字节的情况
- **error.go**: 在 `NFS3Error()` 中添加缺失的 `NFS3ErrAcces` → `os.ErrPermission` 映射
- **rpc/client.go**: 修复 `disconnect()` 关闭 channel 后未清空 `replies` map 的问题
- **mount.go**: 添加缺失的错误码处理：`MNT3ErrInval`、`MNT3ErrNotSupp`、`MNT3ErrServerFault`

### 代码质量改进
- **file.go**: 移除 `ReadlinkRes` 结构体中未使用的私有 `data` 字段
- **target.go**: 移除 `cleanupCache()` 中不必要的 `ticker.Reset()` 调用
- **target.go**: 删除未使用的 `isCacheValid()` 函数
- **target.go**: 修复 `lookup2()` 函数的错误注释
- **example/test/main.go**: 将已弃用的 `io/ioutil` 替换为 `io` 包