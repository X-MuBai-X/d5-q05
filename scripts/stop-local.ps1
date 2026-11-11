<#
.SYNOPSIS
    停止本机部署的题目 05 服务与模拟上游（回退脚本）。

.DESCRIPTION
    读取 deploy\server.pid 与 deploy\mockupstream.pid，停止对应进程。
    这就是本项目部署的「撤销 / 回退」办法。
#>
$ErrorActionPreference = 'Continue'
$repo = Split-Path -Parent $PSScriptRoot
$deploy = Join-Path $repo 'deploy'

foreach ($name in @('server', 'mockupstream')) {
    $pidFile = Join-Path $deploy "$name.pid"
    if (-not (Test-Path $pidFile)) {
        Write-Output "[stop-local] 未找到 $name.pid，跳过"
        continue
    }
    $procId = [int](Get-Content $pidFile | Select-Object -First 1)
    try {
        Stop-Process -Id $procId -Force -ErrorAction Stop
        Write-Output "[stop-local] 已停止 $name（PID=$procId）"
    } catch {
        Write-Output "[stop-local] $name（PID=$procId）未在运行"
    }
}
Write-Output "[stop-local] 完成。数据保留在 deploy\data\ 下，如需回到初始预置状态请删除对应 .db 文件。"
