#!/usr/bin/env pwsh
<#
.SYNOPSIS
  按速率阶梯压测 api，逐档打印 QPS / 延迟分位数 / 成功率。

.DESCRIPTION
  vegeta 的 -rate 是开环（固定速率），这才是真实流量的形状。
  固定并发数的闭环压测会在系统变慢时自动减小压力，把问题掩盖掉
  （coordinated omission）。

.PREREQUISITE
  1. 安装 vegeta：go install github.com/tsenart/vegeta/v12@latest
  2. 压测基线要放开限流，否则测到的是限流器的天花板而不是系统能力：
       $env:RATE_LIMIT_ENABLED='false'; docker compose up -d api

.EXAMPLE
  ./deploy/loadtest/run.ps1 -Target video_detail -Rates 500,1000,2000,3000,4000
#>
[CmdletBinding()]
param(
    [ValidateSet('livez', 'hot', 'video_detail')]
    [string]$Target = 'video_detail',

    [int[]]$Rates = @(500, 1000, 2000),

    [string]$Duration = '10s',

    [string]$BaseUrl = 'http://127.0.0.1:18080',

    [string]$OutDir,

    [string]$Vegeta = (Join-Path $env:USERPROFILE 'go\bin\vegeta.exe')
)

$ErrorActionPreference = 'Stop'

if (-not (Test-Path $Vegeta)) {
    throw "找不到 vegeta：$Vegeta。先执行 go install github.com/tsenart/vegeta/v12@latest"
}
if (-not $OutDir) {
    $OutDir = Join-Path $PSScriptRoot '../../.cache/loadtest'
}
if (-not (Test-Path $OutDir)) {
    New-Item -ItemType Directory -Path $OutDir -Force | Out-Null
}

# 把 targets 文件里的默认地址换成 -BaseUrl，方便压不同环境
$template = Join-Path $PSScriptRoot "$Target.targets"
$targets = Join-Path $OutDir "$Target.generated.targets"
$content = (Get-Content $template -Raw) -replace 'http://127\.0\.0\.1:18080', $BaseUrl
Set-Content -Path $targets -Value $content -NoNewline

Write-Host "目标: $Target    地址: $BaseUrl    每档时长: $Duration`n"

foreach ($rate in $Rates) {
    $out = Join-Path $OutDir "$Target`_$rate.bin"
    & $Vegeta attack -targets $targets -rate $rate -duration $Duration -output $out | Out-Null

    Write-Host "===== $Target @ $rate rps ====="
    & $Vegeta report $out | Select-String -Pattern '^Requests|^Latencies|^Success|^Status Codes|^Error Set'
    Write-Host ""
}
