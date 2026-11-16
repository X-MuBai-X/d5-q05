<#
.SYNOPSIS
    本机真实进程部署脚本（Windows / PowerShell）。

.DESCRIPTION
    构建服务端与模拟上游 → 后台启动两个真实进程 → 健康检查 → 执行业务冒烟 →
    把完整过程写入 evidence/deployment.log。

    这是本项目「教学验收环境」的落地方式：本机真实进程 + 可访问服务地址
    http://127.0.0.1:8080，不是 mock、不是容器编排。

    编码约定（v2.8.0 起强制，见 AGENTS.md §8 第 8 条）：
    Windows 上 PowerShell 5.1 采集原生命令（git / bash）输出时，是按
    [Console]::OutputEncoding 解码的；控制台代码页默认 GBK(936)，会把 UTF-8 中文
    解成串码（乱码），而且这个过程**不可逆**（非法字节被替换成 '?'）。
    因此本脚本一律不依赖控制台代码页：原生命令的输出重定向到临时文件，
    再用 UTF-8 显式读取；日志文件也统一以 UTF-8（无 BOM）追加。

.PARAMETER Port
    服务端口，默认 8080。

.PARAMETER UpstreamPort
    模拟上游端口，默认 9091。

.PARAMETER DbPath
    SQLite 数据库路径，默认 ./deploy/data/app.db。

.PARAMETER Commit
    本次部署对应的提交标识（合并后部署时传入合并提交 SHA）。
    留空时取当前工作区的 HEAD。无论哪种取值都会写入部署日志头部，
    使「部署的是哪个版本」可被逐条核对。

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File scripts/deploy-local.ps1
#>
param(
    [int]$Port = 8080,
    [int]$UpstreamPort = 9091,
    [string]$DbPath = "",
    [string]$LogFile = "",
    [string]$Commit = "",
    [switch]$SkipSmoke,
    [switch]$KeepRunning,
    [switch]$ResetDemo
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

# 日志统一用 UTF-8（无 BOM）追加：避免 PowerShell 5.1 的 `-Encoding utf8` 产生 BOM，
# 也避免任何默认代码页参与写入。
$script:Utf8NoBom = New-Object System.Text.UTF8Encoding($false)

function Read-Utf8Text([string]$path) {
    # 显式按 UTF-8 读取文件内容；不依赖任何代码页推断。
    if ([string]::IsNullOrWhiteSpace($path) -or -not (Test-Path -LiteralPath $path)) { return '' }
    return [System.IO.File]::ReadAllText($path, [System.Text.Encoding]::UTF8)
}

function Write-Log([string]$text) {
    $line = "[{0}] {1}" -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss zzz'), $text
    Write-Output $line
    [System.IO.File]::AppendAllText($deployLog, $line + "`r`n", $script:Utf8NoBom)
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
    # 部署提交标识：优先用调用方传入的 -Commit（合并后部署时传入合并提交 SHA），
    # 否则取当前工作区的 HEAD。写入部署日志，使「部署的是哪个版本」可被逐条核对
    # （scripts/verify-delivery.py 的 D-07 校验即依赖日志中出现合并提交 SHA）。
    $deployCommit = if ([string]::IsNullOrWhiteSpace($Commit)) {
        (& git -C $repo rev-parse HEAD 2>$null)
    } else {
        $Commit
    }
    if ([string]::IsNullOrWhiteSpace($deployCommit)) { $deployCommit = "(未知)" }
    Write-Log "部署提交：$deployCommit"
    # 提交标题走「子进程重定向到文件 + 显式 UTF-8 读取」，
    # 避免中文标题被控制台代码页解成串码（历史日志里的乱码就是从这里来的）。
    $titleFile = Join-Path $env:TEMP ("d5-git-title-" + [guid]::NewGuid().ToString('N') + ".txt")
    try {
        $titleProc = Start-Process -FilePath 'git' `
            -ArgumentList @('-c', 'i18n.logOutputEncoding=utf-8', '-C', ('"{0}"' -f $repo), 'log', '-1', '--format=%s', $deployCommit) `
            -RedirectStandardOutput $titleFile -RedirectStandardError "$titleFile.err" `
            -NoNewWindow -Wait -PassThru
        $deployTitle = (Read-Utf8Text $titleFile).Trim()
    } catch {
        $deployTitle = "(读取提交标题失败：$($_.Exception.Message))"
    } finally {
        Remove-Item -LiteralPath $titleFile, "$titleFile.err" -Force -ErrorAction SilentlyContinue
    }
    Write-Log "部署提交标题：$deployTitle"

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
    $serverArgs = @('-addr', "127.0.0.1:$Port", '-db', $DbPath, '-upstream', "http://127.0.0.1:$UpstreamPort", '-enable-test-hooks')
    if ($ResetDemo) {
        # 显式演示重置：把预置租户的额度与状态恢复成题面基线。
        # 默认不加 —— 重启不得改写已有业务数据（共同要求第 1 节第 3 条）。
        $serverArgs += '-reset-demo'
        Write-Log "已指定 -reset-demo：本次启动会把预置租户重置回题面基线"
    }
    $serverProc = Start-Process -FilePath (Join-Path $binDir 'server.exe') `
        -ArgumentList $serverArgs `
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
    # 冒烟输出同样走「重定向到文件 + 显式 UTF-8 读取」：
    # bash 直接把 UTF-8 字节写进文件，PowerShell 不参与解码，因此不会串码；
    # 退出码仍取自 bash 进程本身。
    $bash = Get-Command bash -ErrorAction SilentlyContinue
    $gitBash = 'C:\Program Files\Git\bin\bash.exe'
    $bashExe = if (Test-Path $gitBash) { $gitBash } elseif ($bash) { $bash.Source } else { '' }
    if ([string]::IsNullOrWhiteSpace($bashExe)) {
        $smoke = @('未找到 bash，跳过冒烟脚本（CI 中由 ubuntu runner 执行）')
        $smokeExit = 2
    } else {
        $smokeFile = Join-Path $env:TEMP ("d5-smoke-" + [guid]::NewGuid().ToString('N') + ".log")
        $smokeFilePosix = $smokeFile -replace '\\', '/'
        $repoPosix = $repo -replace '\\', '/'
        $bashCmd = "cd '$repoPosix' && BASE_URL=http://127.0.0.1:$Port bash scripts/smoke-test.sh > '$smokeFilePosix' 2>&1"
        try {
            if ($bashExe -eq $gitBash) {
                & $gitBash -lc $bashCmd
            } else {
                & bash -lc $bashCmd
            }
            $smokeExit = $LASTEXITCODE
        } finally {
            $smokeText = Read-Utf8Text $smokeFile
            Remove-Item -LiteralPath $smokeFile -Force -ErrorAction SilentlyContinue
        }
        $smoke = @()
        foreach ($l in ($smokeText -split "`r?`n")) {
            if ($null -ne $l) { $smoke += $l.TrimEnd() }
        }
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
