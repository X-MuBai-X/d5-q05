#!/usr/bin/env sh
# 竞态检测（go test -race）执行脚本。
#
# 为什么需要这个脚本：
#   本机 Windows 环境 CGO_ENABLED=0 且没有可用的 C 编译器，
#   `go test -race` 需要 cgo（Go 的竞态检测器依赖 C 运行时）。
#   因此改在官方 golang 镜像（自带 gcc）里执行，
#   挂载当前仓库到 /work，Go 依赖走 goproxy.cn 镜像。
#
# 用法（在仓库根目录执行）：
#   docker run --rm -v "$PWD:/work" -v quota-gomodcache:/go/pkg/mod -w /work \
#     -e GOPROXY=https://goproxy.cn,direct -e CGO_ENABLED=1 \
#     golang:1.27 sh scripts/race-test.sh
#
# 输出：原始执行日志写入 evidence/race-test.log（含 go 版本、退出码）。
# 退出码：0 = 未检出数据竞争；非 0 = 失败或检出竞争。

set -u

OUT="${RACE_LOG:-/work/evidence/race-test.log}"
mkdir -p "$(dirname "$OUT")"

{
    echo "=== go test -race 竞态检测（容器内 linux/amd64） ==="
    echo "执行时间(UTC): $(date -u '+%Y-%m-%dT%H:%M:%SZ')"
    echo "go version: $(go version)"
    echo "GOOS/GOARCH/CGO_ENABLED:"
    go env GOOS GOARCH CGO_ENABLED
    echo "命令: go test ./... -count=1 -race -v"
    echo

    go test ./... -count=1 -race -v 2>&1
    code=$?

    echo
    echo "exit_code=$code"
    if [ "$code" -eq 0 ]; then
        echo "结论：全部包通过，未检出数据竞争。"
    else
        echo "结论：失败或检出数据竞争，请查看上方原始输出。"
    fi
    exit "$code"
} | tee "$OUT"

exit "${PIPESTATUS:-0}"
