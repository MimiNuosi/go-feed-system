# 阶段 2：用户注册、登录与 JWT 鉴权

## 阶段目标

完成以下最小业务闭环：

```text
注册 -> 登录取得 JWT -> 携带 JWT 访问当前用户接口
```

本阶段暂不实现邮箱验证码和服务端主动吊销 token。退出登录先由客户端删除 token，
服务端吊销逻辑留到 Redis 阶段。

## 三个小步

### 2.1 用户表与 Repository

- 建立 users 表。
- 定义 User 模型。
- 定义 Repository 接口。
- 你实现 GORM 的 Create、FindByEmail、FindByID。

### 2.2 注册、登录与 JWT

- 注册时使用 bcrypt 生成 password_hash。
- 登录时使用 bcrypt 比较密码。
- 登录成功签发 HS256 JWT。
- JWT 至少包含 sub、jti、iat、exp、iss。

### 2.3 鉴权中间件与当前用户接口

- 解析 `Authorization: Bearer <token>`。
- 校验签名和过期时间。
- 把当前用户 ID 放进 request context。
- 实现 `GET /api/v1/users/me`。

## 计划接口

注册：

```http
POST /api/v1/auth/register
Content-Type: application/json

{
  "username": "alice",
  "email": "alice@example.com",
  "password": "plain-password"
}
```

登录：

```http
POST /api/v1/auth/login
Content-Type: application/json

{
  "email": "alice@example.com",
  "password": "plain-password"
}
```

当前用户：

```http
GET /api/v1/users/me
Authorization: Bearer <jwt>
```

## 建表 SQL

按照项目约束，Codex 不自动修改 migrations。你需要自己把下面的 SQL 保存为迁移文件并执行。

```sql
CREATE TABLE users (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
    username VARCHAR(32) NOT NULL,
    email VARCHAR(254) NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3)
        ON UPDATE CURRENT_TIMESTAMP(3),
    PRIMARY KEY (id),
    UNIQUE KEY uk_users_username (username),
    UNIQUE KEY uk_users_email (email)
) ENGINE=InnoDB
  DEFAULT CHARACTER SET utf8mb4
  COLLATE utf8mb4_0900_ai_ci;
```

## 当前任务

1. 自己创建并执行 users 表迁移，不要使用 GORM AutoMigrate。
2. 完成 `internal/user/repository.go` 中的三个 TODO。
3. 重点理解为什么“先查询是否存在，再插入”无法单独保证并发安全。
4. 为 Repository 补充测试思路，但暂时不需要连接真实 MySQL。

## C++ 对照

你的 C++ 项目通过 MysqlManager 和 MysqlDAO 完成用户读写；Go 代码将这部分
收口到 Repository。Handler 不直接调用 GORM，类似 C++ handler 不直接操作
mysql_connection，而是经过 DAO。

## 面试追问

1. 为什么并发请求可能绕过“先查询再插入”的重复检查？
2. 最终由唯一索引返回冲突时，Service 应该如何转换为稳定的业务错误码？
3. 为什么查询不到用户时，不应该把 gorm.ErrRecordNotFound 直接返回给前端？
