# Go Feed System

一个用于学习 Go 后端工程的短视频 Feed 流项目。

## 当前阶段

阶段 2：用户注册、登录和 JWT 鉴权。

## 运行

启动前需要设置 MySQL 和 JWT 配置：

```powershell
$env:MYSQL_DSN = "feed_app:你的密码@tcp(127.0.0.1:3308)/go_feed_system?charset=utf8mb4&parseTime=True&loc=Local"
$env:JWT_SECRET = "至少32字节的随机密钥"
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

学习记录见：

- [阶段 0](docs/00-walking-skeleton.md)
- [阶段 1](docs/01-http-layering.md)
- [阶段 2](docs/02-user-auth.md)
