# 阶段 0：让服务正确启动和退出

## 本阶段目标

先不接数据库，也不写业务。只完成一个可以观察、可以测试、可以正确关闭的 HTTP 服务。

完成后应具备：

- 配置从 `pkg/config` 显式加载。
- 使用 `log/slog` 输出结构化日志。
- `GET /livez` 和 `GET /readyz` 返回 JSON。
- 收到 `Ctrl+C` 或 `SIGTERM` 后停止接收新请求，并等待正在处理的请求结束。
- 有健康检查测试。
- `gofmt`、`go vet ./...`、`go test ./...` 全部通过。

## 你的任务

1. 补全 `internal/health/handler_test.go` 中的两个测试。
2. 补全 `cmd/api/main.go` 中的优雅退出逻辑。
3. 实际运行服务，分别请求 `/livez` 和 `/readyz`。
4. 在请求处理过程中按一次 `Ctrl+C`，观察服务如何退出。
5. 写下你对三个 `TODO` 问题的回答。

## Go 与 C++ 对照

### context

Go 的 `context.Context` 表示一次调用的生命周期和取消信号。它接近 C++ 里贯穿异步调用链的取消令牌，但你不需要手工在每个回调里到处检查状态。

在 Asio 项目中，取消一个异步操作、定时器或连接时，你会把停止意图传到相关对象；Go 通常把 `context` 作为跨函数调用的第一个参数传递。

### goroutine

`go f()` 类似把 `f` 投递到线程池，但 goroutine 不是操作系统线程，调度由 Go runtime 管理。它更轻，但“轻”不代表不会泄漏。

当前阶段要保证：启动 HTTP 服务的 goroutine 最终一定能结束，并且发送结果时不会永久阻塞。

### error

Go 使用返回值传错误，通常写成：

```go
if err != nil {
    return fmt.Errorf("load config: %w", err)
}
```

`%w` 类似保留 C++ 异常链里的底层原因，让上层既能判断具体类型，也能保留原始错误。这里不用异常，也不应该用 `panic` 控制正常流程。

## 面试追问

1. 为什么 readiness 失败不应该触发重启？
2. 如果 MySQL 短暂抖动，readiness 返回什么更合理？
3. `server.Shutdown` 一直无法结束，服务是否应该永久等待？
