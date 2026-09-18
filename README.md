# Go Feed System

一个用于学习 Go 后端工程的短视频 Feed 流项目。

## 当前阶段

阶段 0：搭建最小可运行服务，学习配置、日志、健康检查和优雅退出。

## 运行

```powershell
go run ./cmd/api
```

默认监听 `127.0.0.1:8080`，可通过 `HTTP_ADDR` 覆盖：

```powershell
$env:HTTP_ADDR = "127.0.0.1:9090"
go run ./cmd/api
```

## 检查服务

```powershell
curl.exe -i http://127.0.0.1:8080/livez
curl.exe -i http://127.0.0.1:8080/readyz
```

## 常用命令

```powershell
gofmt -w .
go vet ./...
go test ./...
```

阶段 0 的练习与验收标准见 [docs/00-walking-skeleton.md](docs/00-walking-skeleton.md)。
