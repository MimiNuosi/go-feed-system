<#
演示手册用的端到端走查脚本。

它会当场注册两个账号（作者 A / 观看者 B），用 B 的身份把控制台依赖的
每一个接口都调一遍，逐步打印真实结果，最后汇总通过/失败数。
用途有两个：一是面试演示前确认环境是好的，二是把 README 里的说法
变成可复制的证据。

依赖 PowerShell 7（用到 Invoke-RestMethod -Form 提交 multipart）。

用法：
    pwsh -File deploy/demo/walkthrough.ps1 -SampleVideo D:\path\to\clip.mp4
#>

param(
    [string]$BaseUrl = 'http://127.0.0.1:18080',
    [string]$PrometheusUrl = 'http://127.0.0.1:19090',
    [Parameter(Mandatory = $true)][string]$SampleVideo
)

$ErrorActionPreference = 'Stop'

$api = "$BaseUrl/api/v1"
if (-not (Test-Path -LiteralPath $SampleVideo)) {
    throw "找不到样本视频：$SampleVideo"
}
$sample = Get-Item -LiteralPath $SampleVideo

$stamp = Get-Random -Minimum 10000 -Maximum 99999
$authorName = "demoA$stamp"
$viewerName = "demoB$stamp"
$password = 'StrongPass123'

$passed = @()
$failed = @()

function Step($title, [scriptblock]$body) {
    try {
        $result = & $body
        Write-Host ("PASS  {0} :: {1}" -f $title, $result)
        $script:passed += $title
    } catch {
        Write-Host ("FAIL  {0} :: {1}" -f $title, $_.Exception.Message)
        $script:failed += $title
    }
}

function NewAccount($username) {
    $body = @{
        username = $username
        email    = "$username@example.com"
        password = $password
    } | ConvertTo-Json
    return Invoke-RestMethod -Method Post -Uri "$api/auth/register" `
        -ContentType 'application/json' -Body $body
}

function Login($username) {
    $body = @{ email = "$username@example.com"; password = $password } | ConvertTo-Json
    $response = Invoke-RestMethod -Method Post -Uri "$api/auth/login" `
        -ContentType 'application/json' -Body $body
    return @{ Authorization = "Bearer $($response.access_token)" }
}

Write-Host '=== 基础设施 ==='

Step 'GET /livez' {
    $r = Invoke-WebRequest "$BaseUrl/livez" -UseBasicParsing
    "HTTP $($r.StatusCode)"
}

Step 'GET /readyz' {
    $r = Invoke-RestMethod "$BaseUrl/readyz"
    "HTTP 200 status=$($r.status)"
}

Step 'GET /metrics' {
    $r = Invoke-WebRequest "$BaseUrl/metrics" -UseBasicParsing
    $lines = ($r.Content -split "`n" | Where-Object { $_ -match '^[a-z]' }).Count
    "HTTP $($r.StatusCode) 指标行=$lines"
}

Step 'Prometheus 抓取目标' {
    $t = Invoke-RestMethod "$PrometheusUrl/api/v1/targets"
    ($t.data.activeTargets | ForEach-Object { "$($_.labels.job)=$($_.health)" }) -join ' '
}

Step '最小控制台可访问' {
    $r = Invoke-WebRequest "$BaseUrl/" -UseBasicParsing
    "HTTP $($r.StatusCode) bytes=$($r.Content.Length)"
}

Write-Host '=== 作者 A：注册、上传、被发现 ==='

$authorHeaders = $null
$authorID = 0
$videoID = 0

Step '注册作者账号 A' {
    $r = NewAccount $authorName
    $script:authorID = $r.user.id
    "id=$($r.user.id) username=$($r.user.username)"
}

Step 'A 登录' {
    $script:authorHeaders = Login $authorName
    'token 已获取'
}

Step 'GET /users/me' {
    $r = Invoke-RestMethod -Uri "$api/users/me" -Headers $authorHeaders
    "id=$($r.id) username=$($r.username)"
}

Step '上传视频（multipart + 内容嗅探）' {
    $form = @{
        title       = "演示视频 $stamp"
        description = 'walkthrough.ps1 上传的样本'
        file        = $sample
    }
    $r = Invoke-RestMethod -Method Post -Uri "$api/videos" -Headers $authorHeaders -Form $form
    $script:videoID = $r.id
    "id=$($r.id) status=$($r.status) size=$($r.size_bytes)"
}

Step '等状态转 ready 后查详情' {
    for ($i = 0; $i -lt 20; $i++) {
        $r = Invoke-RestMethod -Uri "$api/videos/$videoID" -Headers $authorHeaders
        if ($r.status -eq 'ready') { break }
        Start-Sleep -Milliseconds 500
    }
    "status=$($r.status) view_count=$($r.view_count) author=@$($r.author.username)"
}

Step 'GET /feed/latest 命中刚发布的视频' {
    $r = Invoke-RestMethod -Uri "$api/feed/latest?page_size=5" -Headers $authorHeaders
    $hit = $r.items | Where-Object { $_.id -eq $videoID }
    if (-not $hit) { throw '最新频道里没有刚上传的视频' }
    "items=$($r.items.Count) 命中=$($hit.id)"
}

Step 'GET /videos/:id/file 支持 Range' {
    $r = Invoke-WebRequest -Uri "$api/videos/$videoID/file" `
        -Headers @{ Range = 'bytes=0-1023' } -UseBasicParsing
    "HTTP $($r.StatusCode) bytes=$($r.RawContentLength)"
}

Write-Host '=== 观看者 B：浏览、点赞、评论、关注 ==='

$viewerHeaders = $null

Step '注册并登录观看者账号 B' {
    $null = NewAccount $viewerName
    $script:viewerHeaders = Login $viewerName
    'B 已登录，后续互动都用 B 的身份'
}

Step 'POST /videos/:id/view' {
    $r = Invoke-WebRequest -Method Post -Uri "$api/videos/$videoID/view" `
        -Headers $viewerHeaders -UseBasicParsing
    "HTTP $($r.StatusCode)"
}

Step '等一个回写周期后播放量 +1' {
    Start-Sleep -Seconds 7
    $r = Invoke-RestMethod -Uri "$api/videos/$videoID" -Headers $viewerHeaders
    if ($r.view_count -lt 1) { throw "view_count 仍是 $($r.view_count)" }
    "view_count=$($r.view_count)"
}

Step '同一用户重复浏览被 5 分钟去重' {
    $null = Invoke-WebRequest -Method Post -Uri "$api/videos/$videoID/view" `
        -Headers $viewerHeaders -UseBasicParsing
    Start-Sleep -Seconds 7
    $r = Invoke-RestMethod -Uri "$api/videos/$videoID" -Headers $viewerHeaders
    "view_count=$($r.view_count)（应保持不变）"
}

Step 'POST /videos/:id/like' {
    $r = Invoke-WebRequest -Method Post -Uri "$api/videos/$videoID/like" `
        -Headers $viewerHeaders -UseBasicParsing
    $detail = Invoke-RestMethod -Uri "$api/videos/$videoID" -Headers $viewerHeaders
    "HTTP $($r.StatusCode) like_count=$($detail.like_count) is_liked=$($detail.is_liked_by)"
}

Step 'POST /videos/:id/comments' {
    $body = @{ content = '演示手册评论' } | ConvertTo-Json
    $r = Invoke-RestMethod -Method Post -Uri "$api/videos/$videoID/comments" `
        -Headers $viewerHeaders -ContentType 'application/json' -Body $body
    $list = Invoke-RestMethod -Uri "$api/videos/$videoID/comments"
    "comment id=$($r.id) 列表条数=$($list.items.Count)"
}

Step 'POST /users/:id/follow' {
    $r = Invoke-WebRequest -Method Post -Uri "$api/users/$authorID/follow" `
        -Headers $viewerHeaders -UseBasicParsing
    "HTTP $($r.StatusCode)"
}

Step 'GET /feed/following 命中关注作者的作品' {
    $r = Invoke-RestMethod -Uri "$api/feed/following" -Headers $viewerHeaders
    $hit = ($r.items | Where-Object { $_.id -eq $videoID }).Count
    if ($hit -lt 1) { throw '关注 Feed 里没有命中' }
    "items=$($r.items.Count) 命中=$hit"
}

Step 'GET /feed/hot?window=1h' {
    $r = Invoke-RestMethod -Uri "$api/feed/hot?window=1h&page_size=10" -Headers $viewerHeaders
    "items=$($r.items.Count) 命中=$(($r.items | Where-Object { $_.id -eq $videoID }).Count)"
}

Step 'GET /users/:id/videos' {
    $r = Invoke-RestMethod -Uri "$api/users/$authorID/videos" -Headers $viewerHeaders
    "items=$($r.items.Count)"
}

Write-Host ''
Write-Host ("通过 {0} 步，失败 {1} 步" -f $passed.Count, $failed.Count)
if ($failed.Count -gt 0) {
    Write-Host ("失败项：" + ($failed -join ' / '))
    exit 1
}
Write-Host "本次演示账号：$authorName@example.com / $viewerName@example.com，密码都是 $password"
Write-Host "新视频 id=$videoID（浏览器打开 $BaseUrl/ 即可看到）"
