# CLAUDE.md

本文件为 Claude Code (claude.ai/code) 在此仓库中工作时提供指导。

## 构建和测试命令

```bash
# 构建所有包
go build ./...

# 运行所有测试
go test ./...

# 运行指定包的测试
go test ./nfs/xdr

# 运行集成测试示例（需要 NFS 服务器）
go run ./nfs/example/test <主机>:<导出路径> <测试目录>
```

## 架构概览

这是一个 Go 语言实现的 NFS v3 客户端库，遵循 RFC 1813 (NFS3 协议) 和 RFC 1057 (ONC RPC)。

### 包结构

- **`nfs/`** - 主要 NFS 客户端实现
  - `target.go` - `Target` 类型，表示已挂载的 NFS 卷；处理所有文件操作并支持目录缓存
  - `mount.go` - MOUNT 协议客户端，用于挂载/卸载 NFS 导出
  - `file.go` - `File` 类型，实现 `io.ReadWriteSeeker` 接口用于文件 I/O
  - `nfs.go` - NFS3 协议常量、类型定义和服务拨号函数
  - `error.go` - NFS3 状态码和错误映射

- **`nfs/rpc/`** - ONC RPC 层
  - `client.go` - 异步 RPC 客户端，支持基于 XID 的请求/响应匹配、自动重连和可配置超时（默认 5 秒）
  - `tcp.go` - TCP 传输层，实现 RPC 记录标记（RFC 1831）
  - `portmap.go` - Portmapper 客户端（端口 111），用于服务发现

- **`nfs/xdr/`** - XDR 编码/解码封装，基于 `github.com/rasky/go-xdr`

### 连接流程

1. 查询端口 111 上的 portmapper 以发现 MOUNT 服务端口
2. 调用 MOUNT 协议进行认证并获取根文件句柄
3. 查询 portmapper 获取 NFS 服务端口
4. 使用文件句柄创建 `Target` 进行 NFS 操作

### 核心类型

- **`Target`** - 已挂载的 NFS 卷。持有根文件句柄、认证凭据和目录缓存。方法包括：`Lookup`、`ReadDirPlus`、`Open`、`OpenFile`、`Create`、`Mkdir`、`Remove`、`RemoveAll`、`RmDir`、`Rename`、`Symlink`

- **`File`** - 打开的文件句柄。实现 `io.ReadWriteSeeker`、`io.Closer`、`io.ReaderAt` 接口。在 `Close()` 时调用 NFS3 COMMIT。

- **`Mount`** - MOUNT 协议客户端。方法包括：`Mount`、`Unmount`

### XDR 联合类型

XDR 可区分联合类型使用结构体标签表示：

```go
type PostOpFH3 struct {
    IsSet bool   `xdr:"union"`
    FH    []byte `xdr:"unioncase=1"`
}
```

### RPC 认证

- `rpc.AuthNull` - 空认证 (AUTH_NULL)
- `rpc.NewAuthUnix(machinename, uid, gid)` - Unix 认证 (AUTH_UNIX)

### 目录缓存

`Target` 维护一个目录项缓存，过期时间可配置（`entryTimeout`）。在执行修改操作（创建、删除、重命名等）时缓存会失效。