#!/usr/bin/env bash
# 私有部署授权密钥初始化（发行方 / 签发环境专用，**不要在客户机器上跑**）。
#
# 干什么：
#   1. 生成一对 Ed25519 密钥（公钥给服务验签，私钥用来签客户许可证）；
#   2. 把公钥写进运行配置（默认 data/runtime.env）的 LICENSE_PUB；
#   3. 把私钥只落到本机一个 0600 文件里，供后续 license sign 取用。
#
# 为什么单独一个脚本：`license keygen` 只把密钥打到标准输出，接下来两步靠手工——
# 公钥手工粘进环境变量（粘错一位 = 所有许可证验不过）、私钥手工存文件
# （存成 0644 或顺手提交进仓库 = 授权机制形同虚设）。这两步正是最容易出事的地方。
#
# 用法：
#   scripts/license_setup.sh [--config <配置文件>] [--priv-out <私钥文件>] [--force]
#
# 两个「不可逆」默认拦住，确要这么做请显式加 --force：
#   - 换公钥 ⇒ 旧私钥签的**所有**许可证立刻失效，必须给每个客户重签；
#   - 覆盖私钥文件 ⇒ 更彻底：那把私钥签过的许可证从此无法再续签/重签同样的内容。
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(dirname "$SCRIPT_DIR")"
BACKEND="$ROOT/backend"

CONFIG="${CONFIG_FILE:-$ROOT/data/runtime.env}"
PRIV_OUT="$ROOT/license-private.key"
FORCE=0

usage() {
	cat <<'USAGE'
用法：scripts/license_setup.sh [选项]

  --config PATH    运行配置文件，默认 $CONFIG_FILE 或 ./data/runtime.env
  --priv-out PATH  私钥落盘路径，默认 ./license-private.key
  --force          允许覆盖已存在的 LICENSE_PUB / 私钥文件（见脚本头部的不可逆提醒）
  -h, --help       显示本说明

跑完之后：
  1. 私钥留在签发环境，绝不进仓库 / 客户机器 / 服务二进制；
  2. 把 data/license.key 与 LICENSE_PUB 一起交付给客户（私有部署才需要）。
USAGE
}

die() { printf '错误：%s\n' "$1" >&2; exit 1; }

while [ $# -gt 0 ]; do
	case "$1" in
	--config)
		[ $# -ge 2 ] || die "--config 缺少参数"
		CONFIG="$2"
		shift 2
		;;
	--priv-out)
		[ $# -ge 2 ] || die "--priv-out 缺少参数"
		PRIV_OUT="$2"
		shift 2
		;;
	--force) FORCE=1; shift ;;
	-h | --help) usage; exit 0 ;;
	*) die "未知参数 $1（用 -h 看用法）" ;;
	esac
done

command -v go >/dev/null 2>&1 || die "未找到 go：请先安装 Go 工具链"
[ -d "$BACKEND/cmd/license" ] || die "未找到 $BACKEND/cmd/license（请在仓库内运行本脚本）"

# ── 守卫 1：已有的 LICENSE_PUB 不允许静默覆盖 ──
if [ -f "$CONFIG" ] && grep -q '^LICENSE_PUB=' "$CONFIG"; then
	if [ "$FORCE" -ne 1 ]; then
		printf '配置文件 %s 里已经有 LICENSE_PUB。\n' "$CONFIG" >&2
		printf '换公钥会让**旧私钥签的所有许可证立刻失效**，必须给每个客户重签。\n' >&2
		die "确要换钥请加 --force"
	fi
	printf '警告：--force 已指定，正在换公钥——旧许可证将全部失效，记得重签。\n'
fi

# ── 守卫 2：已有的私钥文件不允许静默覆盖 ──
if [ -e "$PRIV_OUT" ]; then
	if [ "$FORCE" -ne 1 ]; then
		printf '私钥文件 %s 已存在。\n' "$PRIV_OUT" >&2
		printf '覆盖它意味着那把私钥签过的许可证再也签不出同样的内容。\n' >&2
		die "确要覆盖请加 --force，或换一个 --priv-out 路径"
	fi
	printf '警告：--force 已指定，正在覆盖已有私钥文件。\n'
fi

# 已有许可证文件时提醒一句（换钥后它必然验不过）
if [ -f "$ROOT/data/license.key" ]; then
	printf '提示：检测到 %s/data/license.key，换公钥后它一定验签失败，之后需重新签发。\n' "$ROOT"
fi

# ── 生成密钥对 ──
# go run 会把编译信息写到 stderr，只取 stdout 的 public / private 两行。
KEYGEN_OUT="$(cd "$BACKEND" && go run ./cmd/license keygen 2>/dev/null)"
PUB="$(printf '%s\n' "$KEYGEN_OUT" | sed -n 's/^public: //p' | tail -n1)"
PRIV="$(printf '%s\n' "$KEYGEN_OUT" | sed -n 's/^private: //p' | tail -n1)"

[ -n "$PUB" ] || die "未能从 keygen 输出里取到公钥（go run 是否失败？）"
[ -n "$PRIV" ] || die "未能从 keygen 输出里取到私钥（go run 是否失败？）"
# Ed25519 公钥 32 字节 → base64 44 字符；私钥 64 字节 → 88 字符。
# 这里做长度校验，避免把注释行或错误信息当成密钥写进配置。
[ "${#PUB}" -eq 44 ] || die "公钥长度异常（${#PUB} 字符，应为 44）"
[ "${#PRIV}" -eq 88 ] || die "私钥长度异常（${#PRIV} 字符，应为 88）"

# ── 私钥落盘：先 umask 077 再创建，避免「先建 0644 再 chmod」那一瞬间的可读窗口 ──
PRIV_DIR="$(dirname "$PRIV_OUT")"
mkdir -p "$PRIV_DIR"
(umask 077 && printf '%s\n' "$PRIV" >"$PRIV_OUT")
chmod 600 "$PRIV_OUT"

# ── 公钥写进配置：先写同目录临时文件再 rename，与 config.WriteFileValues 同样的原子写 ──
CONFIG_DIR="$(dirname "$CONFIG")"
mkdir -p "$CONFIG_DIR"
TMP="$(mktemp "${CONFIG_DIR}/.license_setup.XXXXXX")"
# 已有的同名键整行剔掉：grep 无匹配时返回 1，这里本就是「没有才正常」，不是错误
if [ -f "$CONFIG" ]; then
	grep -v '^LICENSE_PUB=' "$CONFIG" >"$TMP" || true
fi
printf 'LICENSE_PUB=%s\n' "$PUB" >>"$TMP"
chmod 600 "$TMP"
mv -f "$TMP" "$CONFIG"

printf '\n完成。\n'
printf '  公钥已写入：%s （LICENSE_PUB）\n' "$CONFIG"
printf '  私钥已落盘：%s （0600）\n' "$PRIV_OUT"
printf '\n下一步（在**签发环境**签一张客户许可证）：\n'
printf '  cd %s\n' "$BACKEND"
printf '  go run ./cmd/license sign --priv-file %s \\\n' "$PRIV_OUT"
printf '      --tenant <企业id> --seats <席位> --expires 2027-01-01T00:00:00Z \\\n'
printf '      --features mp_release --out data/license.key\n'
printf '\n核对：go run ./cmd/license show --file data/license.key --pub %s\n\n' "$PUB"
printf '注意：**私钥绝不进仓库 / 客户机器 / 服务二进制**；本脚本只在签发环境跑一次。\n'
