# 压测脚本

配套文档：`docs/10-load-testing.md`（含方法论、实测数据和瓶颈分析）。

## 前提

```powershell
# 1. 安装 vegeta
go install github.com/tsenart/vegeta/v12@latest

# 2. 压测基线要放开限流，否则测到的是限流器的天花板而不是系统能力
$env:RATE_LIMIT_ENABLED='false'; docker compose up -d api
```

## 两个脚本

`run.ps1` 做速率阶梯，逐档打印 QPS / 延迟分位数 / 成功率：

```powershell
./deploy/loadtest/run.ps1 -Target video_detail -Rates 500,1000,2000,3000,4000
./deploy/loadtest/run.ps1 -Target livez -Rates 2000,4000        # 天花板参考
```

`sample.ps1` 跑一轮固定速率，同时采样服务端资源，并算出每请求 SQL 语句数：

```powershell
./deploy/loadtest/sample.ps1 -Target video_detail -Rate 4000 -Seconds 10
./deploy/loadtest/sample.ps1 -Target livez -Rate 4000 -Seconds 10   # 对照组
```

## 三个被测目标

```text
livez         纯 Go HTTP 路径（探针，豁免限流）—— 这台机器能发多快的参考
hot           本地缓存 + singleflight，几乎全内存 —— 验证缓存是否真的生效
video_detail  MySQL 查询 + 点赞聚合，无缓存 —— 最接近真实瓶颈的路径
```

## 怎么读结果

```text
看 P95/P99，不要看平均：平均会把长尾藏起来。
看 P50 是否一起上涨：中位数上涨说明队列已经堆积，
  这是饱和（而不是偶发抖动）的信号。
看 Success 与 Status Codes：系统应该用延迟退化，而不是用报错退化。
看 rate 与 throughput 是否一致：一致说明压测客户端把预定速率发出去了，
  瓶颈在服务端；差得远则要先怀疑客户端自己跟不上。
```

## 注意

压测客户端和被压服务跑在同一台机器上，会互相抢 CPU。
判断客户端是否是瓶颈，就看 `Requests` 行的 rate 与 throughput 是否一致。
换机器或换数据量后，拐点位置会变，结论必须带上环境前提。
