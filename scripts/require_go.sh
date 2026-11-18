#!/usr/bin/env sh
# 开发工具统一的 Go 工具链预检（被 tools/*/run.sh、scripts/license_setup.sh source）。
#
# 这些脚本只在开发机 / CI 跑：它们现场 `go run` 或 `go build` 本仓库的 cmd/。
# 生产服务器上没有 Go 工具链（用户实测），而 R1 上线的正确做法是把二进制在开发机
# 交叉编译后 scp 过去（见 docs/tenant-storage-r1-golive.md）。没有这条预检时，
# 缺 go 的表现是「编译日志里一句 command not found」或一堆看不懂的报错，
# 运维会误以为是工具脚本坏了；这里直接说清该用哪条路径。
#
# 用法：. "$(dirname "$0")/../../scripts/require_go.sh"
# 失败时 exit 1 结束当前 shell（source 的场景即终止调用脚本）。

if ! command -v go >/dev/null 2>&1; then
    echo "✗ 本工具需要本机 Go 工具链（现场编译 backend/cmd/*）。" >&2
    echo "  这些工具只在开发机 / CI 运行；生产服务器不需要 Go，" >&2
    echo "  请按 docs/tenant-storage-r1-golive.md 在开发机交叉编译静态二进制后 scp。" >&2
    exit 1
fi
