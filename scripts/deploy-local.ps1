<#
.SYNOPSIS
    本机真实进程部署脚本（Windows / PowerShell）。

.DESCRIPTION
    构建服务端与模拟上游 → 后台启动两个真实进程 → 健康检查 → 执行业务冒烟 →
    把完整过程写入 evidence/deployment.log。

    这是本项目「教学验收环境」的落地方式：本机真实进程 + 可访问服务地址
    http://127.0.0.1:8080，不是 mock、不是容器编排。

.PARAMETER Port
    服务端口，默认 8080。

.PARAMETER UpstreamPort
    模拟上游端口，默认 9091。

.PARAMETER DbPath
    SQLite 数据库路径，默认 ./deploy/data/app.db。

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File scripts/deploy-local.ps1
#>
param(
    [int]$Port = 8080,
    [int]$UpstreamPort = 9091,
    [string]$DbPath = "",
    [string]$LogFile = "",
    [switch]$SkipSmoke,
    [switch]$KeepRunning
)

$ErrorActionPreference = 'Stop'
$repo = Split-Path -Parent $PSScriptRoot
Set-Location $repo

if ([string]::IsNullOrWhiteSpace($DbPath)) {
    $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
    $DbPath = Join-Path $repo "deploy\data\app-$stamp.db"
}

$binDir = Join-Path $repo 'deploy\bin'
$logDir = Join-Path $repo 'deploy'
$evidence = Join-Path $repo 'evidence'
New-Item -ItemType Directory -Force -Path $binDir, (Split-Path -Parent $DbPath), $evidence | Out-Null

$deployLog = if ([string]::IsNullOrWhiteSpace($LogFile)) {
    Join-Path $evidence 'deployment.log'
} else {
    if ([System.IO.Path]::IsPathRooted($LogFile)) { $LogFile } else { Join-Path $repo $LogFile }
}
function Write-Log([string]$text) {
    $line = "[{0}] {1}" -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss zzz'), $text
    Write-Output $line
    Add-Content -Path $deployLog -Value $line -Encoding utf8
}

$serverProc = $null
$upstreamProc = $null

try {
    Write-Log "===== 题目 05 本机真实进程部署开始 ====="
    Write-Log "仓库根目录：$repo"
    Write-Log "Go 版本：$(go version)"
    Write-Log "服务地址：http://127.0.0.1:$Port"
    Write-Log "模拟上游：http://127.0.0.1:$UpstreamPort"
    Write-Log "数据库路径：$DbPath"

    Write-Log "--- 步骤 1/6：检查端口占用 ---"
    # 必须先确认端口空闲：否则新进程启动失败，而健康检查与冒烟会打到旧进程上，
    # 导致「测的不是刚构建的产物」这种假通过或假失败。
    $busy = @()
    foreach ($p in @($Port, $UpstreamPort)) {
        $conn = Get-NetTCPConnection -LocalPort $p -State Listen -ErrorAction SilentlyContinue
        if ($conn) {
            $owner = ($conn | Select-Object -ExpandProperty OwningProcess -Unique) -join ','
            $busy += "$p(PID=$owner)"
        }
    }
    if ($busy.Count -gt 0) {
        throw "端口已被占用：$($busy -join '、')。请先执行 scripts/stop-local.ps1，或手动结束占用进程后重试。"
    }
    Write-Log "端口检查通过：$Port、$UpstreamPort 均空闲"

    Write-Log "--- 步骤 2/6：构建可执行产物 ---"
    & go -C $repo build -o (Join-Path $binDir 'server.exe') ./cmd/server
    if ($LASTEXITCODE -ne 0) { throw "构建 server 失败（退出码 $LASTEXITCODE）" }
    & go -C $repo build -o (Join-Path $binDir 'mockupstream.exe') ./cmd/mockupstream
    if ($LASTEXITCODE -ne 0) { throw "构建 mockupstream 失败（退出码 $LASTEXITCODE）" }
    Write-Log "构建完成：$binDir\server.exe、$binDir\mockupstream.exe"

    Write-Log "--- 步骤 3/6：启动模拟上游模型服务 ---"
    $upstreamProc = Start-Process -FilePath (Join-Path $binDir 'mockupstream.exe') `
        -ArgumentList '-addr', "127.0.0.1:$UpstreamPort" `
        -RedirectStandardOutput (Join-Path $logDir 'mockupstream.log') `
        -RedirectStandardError (Join-Path $logDir 'mockupstream.err.log') `
        -PassThru -WindowStyle Hidden
    Write-Log "模拟上游已启动，PID=$($upstreamProc.Id)"
    # 回退脚本依赖这个 PID 文件：部署成功后必须写，否则 stop-local.ps1 停不掉本次实例。
    Set-Content -Path (Join-Path $logDir 'mockupstream.pid') -Value $upstreamProc.Id -Encoding ascii
    Write-Log "已记录模拟上游 PID 文件：deploy\mockupstream.pid"

    Write-Log "--- 步骤 4/6：启动业务服务 ---"
    $serverProc = Start-Process -FilePath (Join-Path $binDir 'server.exe') `
        -ArgumentList '-addr', "127.0.0.1:$Port", '-db', $DbPath, '-upstream', "http://127.0.0.1:$UpstreamPort", '-enable-test-hooks' `
        -RedirectStandardOutput (Join-Path $logDir 'server.log') `
        -RedirectStandardError (Join-Path $logDir 'server.err.log') `
        -PassThru -WindowStyle Hidden
    Write-Log "业务服务已启动，PID=$($serverProc.Id)"
    Set-Content -Path (Join-Path $logDir 'server.pid') -Value $serverProc.Id -Encoding ascii
    Write-Log "已记录服务 PID 文件：deploy\server.pid（回退：powershell -ExecutionPolicy Bypass -File scripts\stop-local.ps1）"

    Write-Log "--- 步骤 5/6：健康检查 ---"
    $ready = $false
    for ($i = 0; $i -lt 30; $i++) {
        try {
            $resp = Invoke-WebRequest -Uri "http://127.0.0.1:$Port/healthz" -UseBasicParsing -TimeoutSec 3
            if ($resp.StatusCode -eq 200) {
                Write-Log "健康检查通过：$($resp.Content)"
                $ready = $true
                break
            }
        } catch { Start-Sleep -Seconds 1 }
    }
    if (-not $ready) { throw "服务在 30 秒内未通过健康检查" }
    $upResp = Invoke-WebRequest -Uri "http://127.0.0.1:$UpstreamPort/healthz" -UseBasicParsing -TimeoutSec 5
    Write-Log "模拟上游健康检查通过：$($upResp.Content)"

    Write-Log "--- 步骤 6/6：业务冒烟 ---"
    if ($SkipSmoke) {
        Write-Log "已按 -SkipSmoke 跳过业务冒烟（用于五组完整场景验证：需要未污染的全新数据库）"
        Write-Log "===== 部署成功（跳过冒烟）：服务地址 http://127.0.0.1:$Port ====="
        Write-Log "服务 PID=$($serverProc.Id)；模拟上游 PID=$($upstreamProc.Id)"
        Write-Log "恢复办法：停止进程即可回退；数据库为本地 SQLite 文件，删除 $DbPath 即回到初始预置状态"
        return
    }
    $bash = Get-Command bash -ErrorAction SilentlyContinue
    $gitBash = 'C:\Program Files\Git\bin\bash.exe'
    if (Test-Path $gitBash) {
        $smoke = & $gitBash -lc "cd '$($repo -replace '\\','/')' && BASE_URL=http://127.0.0.1:$Port bash scripts/smoke-test.sh" 2>&1
        $smokeExit = $LASTEXITCODE
    } elseif ($bash) {
        $smoke = & bash -lc "cd '$($repo -replace '\\','/')' && BASE_URL=http://127.0.0.1:$Port bash scripts/smoke-test.sh" 2>&1
        $smokeExit = $LASTEXITCODE
    } else {
        $smoke = @('未找到 bash，跳过冒烟脚本（CI 中由 ubuntu runner 执行）')
        $smokeExit = 2
    }
    foreach ($l in $smoke) { Write-Log "  $l" }
    Write-Log "业务冒烟退出码：$smokeExit"

    if ($smokeExit -ne 0) { throw "业务冒烟未通过（退出码 $smokeExit）" }

    Write-Log "===== 部署成功：服务地址 http://127.0.0.1:$Port ====="
    Write-Log "服务 PID=$($serverProc.Id)；模拟上游 PID=$($upstreamProc.Id)"
    Write-Log "恢复办法：停止进程即可回退；数据库为本地 SQLite 文件，删除 $DbPath 即回到初始预置状态"
    if (-not $KeepRunning) {
        Write-Log "（脚本结束，服务进程保持运行；如需停止请执行 scripts/stop-local.ps1）"
    }
}
catch {
    Write-Log "部署失败：$($_.Exception.Message)"
    exit 1
}
finally {
    if (-not $KeepRunning -and $serverProc -and -not $serverProc.HasExited) {
        Write-Log "提示：服务仍在运行（PID=$($serverProc.Id)）。"
    }
}
