# 阶段 1：Gin、中间件和请求标识

## 本阶段目标

保留阶段 0 的服务生命周期，把 HTTP 路由从标准库迁移到 Gin，并建立统一的请求处理链：

```text
HTTP 请求
  -> RequestID
  -> AccessLog
  -> Recovery
  -> 路由匹配
  -> 业务 Handler
  -> HTTP 响应
```

## 为什么使用这个中间件顺序

`RequestID` 必须最先执行，否则后续日志无法获得同一个请求标识。

`AccessLog` 包在最外层。它调用 `c.Next()` 后，后面的 Recovery 和 Handler 才执行。即使 Handler 发生 panic，Recovery 也会先把响应改为 500，然后 AccessLog 再记录最终状态码和耗时。

`Recovery` 放在业务 Handler 前，兜住当前请求处理链中的未知 panic。它不能捕获其他 goroutine 中未处理的 panic，也不能用来处理可预期的业务错误。

注意 Gin 的 `c.Next()` 类似调用下一层中间件，而不是立即返回客户端。中间件代码可以分为两部分：

```text
c.Next() 之前：请求进入业务逻辑之前
c.Next() 之后：业务逻辑结束之后
```

这和 C++ 中 RAII 清理逻辑很像，但 Go 更常使用 `defer` 处理函数退出。

## C++ 对照

### 路由表

你的 C++ 项目使用：

```cpp
std::map<std::string, HttpHandler> _get_handlers;
std::map<std::string, HttpHandler> _post_handlers;
```

Gin 的 `engine.GET` 和 `engine.POST` 概念相同，只是由框架负责匹配方法、路径参数和优先级。

### 中间件

可以把中间件理解为“按顺序包装 Handler 的函数”。概念上类似装饰器：

```cpp
auto handler = Recovery(AccessLog(RequestID(businessHandler)));
```

不过 Gin 使用 `c.Next()` 显式进入下一层，因此日志代码可以在业务执行前后各放一部分。

### 请求 ID

`context.Context` 在这里类似一次请求的上下文对象。Go 更倾向于把请求级数据放进 context，而不是放进全局单例。

## 当前任务

1. 阅读 `internal/middleware/request_id.go`，解释为什么使用 `crypto/rand` 而不是 `math/rand`。
2. 添加测试：请求携带 `X-Request-ID: test-id` 时，响应头应返回同一个值。
3. 添加测试：请求没有携带 `X-Request-ID` 时，响应头应返回非空且互不相同的值。
4. 添加测试：`POST /livez` 的响应是什么，并判断是否应该改成 405。
5. 观察访问日志中是否同时存在 `request_id`、`status` 和 `latency_ms`。

## 验收命令

```powershell
gofmt -w .
go test ./...
go vet ./...
```

## 面试追问

1. 为什么不能无条件信任客户端传入的 `X-Request-ID`？

因为存在日志注入和伪造追踪的安全风险。如果客户端传入了超长字符串或包含日志污染、伪造追踪 ID 等安全风险，可能会撑爆内存或污染日志。这就好比 C++ 在接收网络包时，绝不能无条件信任 payload。所以我们的中间件必须加上长度限制（128）和字符白名单校验，非法的一律丢弃并自动生成。

2. Recovery 中间件能否捕获后台 goroutine 的 panic？

不能。Gin 的 Recovery 只能捕获当前 HTTP 请求处理链（即当前处理请求的goroutine）中的 panic。一旦在业务代码里使用 go func() 开启新协程，而协程内部发生 panic 且没有自行 defer recover()，整个服务进程就会崩溃（类比 C++ 里线程抛出异常未捕获导致 std::terminate）。

3. 为什么日志中间件通常写在 Recovery 的外层？

因为日志中间件需要记录完整的请求生命周期。如果日志在 Recovery 内层，一旦业务发生 panic，Recovery 会截断逻辑并返回 500，日志代码根本没机会执行。写在最外层（c.Next() 之前开启计时），无论内层发生什么，都能准确记录最终的 status 和 latency_ms。