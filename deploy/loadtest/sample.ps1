#!/usr/bin/env pwsh
<#
.SYNOPSIS
  跑一轮固定速率压测，同时采样服务端资源，并算出“每请求 SQL 语句数”。

.DESCRIPTION
  输出三部分：
    1. 压测稳态时容器的 CPU / 内存占用
    2. MySQL 连接数、语句增量，以及每请求语句数
       （这个数字能直接暴露 N+1 查询和多余的数据库往返）
    3. vegeta 的延迟报告

  对照方法：用 -Target livez 跑一遍作基线，它不碰数据库，
  量出来的就是后台任务的噪声。

.EXAMPLE
  ./deploy/loadtest/sample.ps1 -Target video_detail -Rate 4000 -Seconds 10
  ./deploy/loadtest/sample.ps1 -Target livez -Rate 4000 -Seconds 10
#>
[CmdletBinding()]
param(
    [ValidateSet('livez', 'hot', 'video_detail')]
    [string]$Target = 'video_detail',

    [int]$Rate = 4000,

    [int]$Seconds = 10,

    [string]$BaseUrl = 'http://127.0.0.1:18080',

    [string]$OutDir,

    [string]$EnvFile,

    [string]$MysqlContainer = 'go-feed-system-mysql-1',

    [string]$MysqlUser = 'feed_app',

    [string]$Vegeta = (Join-Path $env:USERPROFILE 'go\bin\vegeta.exe')
)

$ErrorActionPreference = 'Stop'

if (-not $OutDir) {
    $OutDir = Join-Path $PSScriptRoot '../../.cache/loadtest'
}
if (-not (Test-Path $OutDir)) {
    New-Item -ItemType Directory -Path $OutDir -Force | Out-Null
}
if (-not $EnvFile) {
    $EnvFile = Join-Path $PSScriptRoot '../../.env'
}

# 数据库口令从 .env 读取，不写进仓库（.env 已被 gitignore）
$passwordLine = Select-String -Path $EnvFile -Pattern '^MYSQL_PASSWORD=' | Select-Object -First 1
if (-not $passwordLine) {
    throw "在 $EnvFile 里找不到 MYSQL_PASSWORD"
}
$password = ($passwordLine.Line -split '=', 2)[1]

$template = Join-Path $PSScriptRoot "$Target.targets"
$targets = Join-Path $OutDir "$Target.generated.targets"
$content = (Get-Content $template -Raw) -replace 'http://127\.0\.0\.1:18080', $BaseUrl
Set-Content -Path $targets -Value $content -NoNewline

$bin = Join-Path $OutDir "$Target`_sample_$Rate.bin"

function Get-MysqlQueries {
    # 注意：PowerShell 里 "-p$pw" 这种紧凑写法不会按预期展开，
    # 必须写成 "--password=$pw" 才会把值传进去。
    $row = & docker exec $MysqlContainer mysql "--user=$MysqlUser" "--password=$password" -N -e "SHOW STATUS LIKE 'Queries';" 2>$null
    $text = ($row | Out-String)
    return [int](($text -replace 'Queries\s+', '').Trim())
}

$before = Get-MysqlQueries

$job = Start-Job -ScriptBlock {
    param($vegeta, $targets, $rate, $duration, $out)
    & $vegeta attack -targets $targets -rate $rate -duration $duration -output $out | Out-Null
} -ArgumentList $Vegeta, $targets, $Rate, "$($Seconds)s", $bin

Start-Sleep -Seconds ([math]::Max(2, [int]($Seconds / 3)))

Write-Host "--- 压测稳态时的容器占用 ---"
docker stats --no-stream --format '{{.Name}}  CPU={{.CPUPerc}}  内存={{.MemUsage}}' |
    Select-String -Pattern 'api|mysql|redis'

Write-Host "`n--- MySQL 连接数 ---"
docker exec $MysqlContainer mysql "--user=$MysqlUser" "--password=$password" -e "SHOW STATUS WHERE Variable_name IN ('Threads_connected','Threads_running');" 2>$null

Wait-Job $job | Out-Null
Remove-Job $job -Force

$after = Get-MysqlQueries
$requests = $Rate * $Seconds
$delta = $after - $before

Write-Host "`n--- SQL 语句 ---"
Write-Host ("语句增量 {0}，每请求约 {1:N2} 条" -f $delta, ($delta / $requests))
Write-Host "（对照组：-Target livez 跑同样速率，它完全不碰数据库）`n"

Write-Host "--- 压测结果 ---"
& $Vegeta report $bin | Select-String -Pattern '^Requests|^Latencies|^Success|^Status Codes|^Error Set'
