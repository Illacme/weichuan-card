#!/usr/bin/env bash
#
# 前端发版：把 frontend/dist 传到宝塔站点目录，并保证「不会出现白屏窗口」。
#
# 为什么需要这个脚本：Vite 产物是 index.html + 一堆带 hash 的 assets。
# 手传时如果先覆盖 index.html，用户会在几秒内拿到新壳、却找不到还没传上去的新资源
# ——浏览器直接 404，页面白屏。反过来做（先 assets 后 index.html）任何时刻都是
# 可用组合：新资源先就位（旧壳用不到它们，无害），最后一步才换壳。
# 这个顺序靠人记不住，交给脚本。
#
# 用法：
#   ./scripts/deploy_frontend.sh                      # 发到默认服务器（root@123.56.154.49）
#   ./scripts/deploy_frontend.sh --build             # 先构建再发
#   DEPLOY_PASSWORD='服务器口令' ./scripts/deploy_frontend.sh --build   # 推荐：非交互，口令不进命令行
#   ./scripts/deploy_frontend.sh --password '服务器口令' --build        # 也能用，但口令会留在 shell history / ps
#   ./scripts/deploy_frontend.sh -n                  # 只看要做什么（dry-run）
#   ./scripts/deploy_frontend.sh --require-migrations  # 远端迁移清单不完整就拒绝发版（CI 用）
#   DEPLOY_HOST=root@其它服务器 ./scripts/deploy_frontend.sh   # 覆盖到别的服务器
#   DEPLOY_HOST= ./scripts/deploy_frontend.sh -n     # 显式置空 = 本地自测模式（不连服务器）
#
# 环境变量：
#   DEPLOY_HOST     ssh 目标，默认 root@123.56.154.49（见 DEFAULT_DEPLOY_HOST）。
#                   留空（DEPLOY_HOST=）= 本地模式（DEPLOY_PATH 当本地目录，仅用于自测）；
#                   设了就覆盖默认值，例如发到临时服务器。
#   DEPLOY_PORT     ssh 端口，默认 22
#   DEPLOY_PATH     远端站点目录，默认 /www/wwwroot/weichuan.ziyuanxingqiu.cn/frontend
#   DEPLOY_MIGRATIONS_DIR
#                   远端 migrations/ 目录，默认取 DEPLOY_PATH 父目录下的 migrations/
#                   （后端二进制与 migrations/ 同级）。路径不同就显式指定；
#                   **显式置空 = 跳过迁移清单比对**，只在你确定不需要这项检查时用。
#   DEPLOY_REQUIRE_MIGRATIONS
#                   设为 1 等同 --require-migrations，CI 里用环境变量更方便。
#   DEPLOY_URL      发版后自检用的地址，默认 https://weichuan.ziyuanxingqiu.cn；留空跳过自检
#   DEPLOY_CHOWN    可选，如 www:www。以 root 发版时建议设置，否则文件属主是 root
#   DEPLOY_SSH_OPTS 可选，追加 ssh 参数
#   DEPLOY_TRANSFER 可选，rsync（增量，默认）/ tar（全量）。远端没装 rsync 会自动用 tar
#   DIST_DIR        可选，默认 ../frontend/dist
#   DEPLOY_PASSWORD 可选，ssh 登录口令。设了就全程非交互，不再提示输密码；
#                   --password 优先于它。**优先用环境变量**：命令行参数会进 shell history / ps。
#
# 选项：
#   -n, --dry-run   只打印要做的事，不改任何文件
#   --build         先跑 npm run build 再发
#   --password PWD  ssh 登录口令，也可写 --password=PWD；用于无人值守发版
#   --no-verify     跳过发版后的自检
#   --require-migrations
#                   远端 migrations/ 清单必须与本版本 backend/migrations/ 一致，
#                   缺文件或读不到就**拒绝发版**（默认只警告、继续发）。
#   --prune[=N]     清理 N 天前的旧 assets（默认 7）；只列出，需输入 yes 才真删
#   -y, --yes       跳过 --prune 的确认（CI 用）
#   -h, --help      显示本帮助
#
# 注意：**不带 --delete**。旧 assets 故意留着——除了保证上面的切换窗口安全，
# 也是后续启用「HTML 边缘缓存」的前提（边缘可能还在发几十秒前的旧壳，
# 旧壳引用的旧 hash 文件必须还在）。要清理用 --prune。
#
# 传输方式自动选：远端有 rsync 走增量（只传变化的文件）；没有就降级 tar（全量，
# 宝塔的 CentOS 最小镜像通常不带 rsync）。tar 分支用 ssh 连接复用，密码只输一次。
# 想彻底免密：ssh-copy-id root@<服务器IP>，之后不再提示。
#
# 给了 DEPLOY_PASSWORD / --password 时全程不提示：优先用 OpenSSH >= 8.4 自带的
# SSH_ASKPASS_REQUIRE=force（零依赖，口令只经环境变量交给 ssh，不写文件、不进 argv、
# 不进日志）；老版本 OpenSSH 且装了 sshpass 时自动改走 sshpass，两者都没有会直接报错，
# 绝不静默卡在交互提示上。ControlMaster 仍生效，探测/上传/自检共用一条连接。
# 注意：**命令行上的口令会留在 shell history 和 ps 里**，日常请用环境变量或免密。
#
set -euo pipefail

# 默认发版目标（你的宝塔服务器）。可用 DEPLOY_HOST 环境变量覆盖，
# 例如：DEPLOY_HOST=root@其它服务器 ./scripts/deploy_frontend.sh
DEFAULT_DEPLOY_HOST=root@123.56.154.49
# 用 ${VAR-default}（单横杠，不是 :-）：DEPLOY_HOST= 显式置空仍进入本地自测模式；
# 只有「完全不传」才回落到上面的默认值。这样日常发版不用打一长串。
DEPLOY_HOST=${DEPLOY_HOST-$DEFAULT_DEPLOY_HOST}
DEPLOY_PORT=${DEPLOY_PORT:-22}
DEPLOY_PATH=${DEPLOY_PATH:-/www/wwwroot/weichuan.ziyuanxingqiu.cn/frontend}
DEPLOY_URL=${DEPLOY_URL-https://weichuan.ziyuanxingqiu.cn}
DEPLOY_CHOWN=${DEPLOY_CHOWN:-}
DEPLOY_SSH_OPTS=${DEPLOY_SSH_OPTS:-}
DEPLOY_PASSWORD=${DEPLOY_PASSWORD:-}
PASSWORD_FROM_CLI=0

DRY_RUN=0
DO_BUILD=0
DO_VERIFY=1
PRUNE_DAYS=""
ASSUME_YES=0
# 迁移清单完整性只在 --require-migrations / 环境变量下变成硬门禁；默认只警告，
# 免得「发前端的机器上没有后端源码」或「迁移由另一条流水线单独部署」被无谓卡住。
REQUIRE_MIGRATIONS=${DEPLOY_REQUIRE_MIGRATIONS:-0}
[ "$REQUIRE_MIGRATIONS" = 1 ] || REQUIRE_MIGRATIONS=0

usage() {
    # 只打印文件顶部那段注释（到第一个非注释行为止），不把代码也打出来
    awk 'NR > 2 { if (/^#/) { sub(/^# ?/, ""); print; next } else { exit } }' "$0"
    exit "${1:-0}"
}

while [ $# -gt 0 ]; do
    case "$1" in
        -n|--dry-run) DRY_RUN=1 ;;
        --build)      DO_BUILD=1 ;;
        --no-verify)  DO_VERIFY=0 ;;
        --require-migrations) REQUIRE_MIGRATIONS=1 ;;
        --prune)      PRUNE_DAYS=7 ;;
        --prune=*)    PRUNE_DAYS=${1#*=} ;;
        --password)   shift
                      [ $# -gt 0 ] || { echo "✗ --password 后面要跟口令（-h 看用法）" >&2; exit 2; }
                      DEPLOY_PASSWORD=$1; PASSWORD_FROM_CLI=1 ;;
        --password=*) DEPLOY_PASSWORD=${1#*=}; PASSWORD_FROM_CLI=1 ;;
        -y|--yes)     ASSUME_YES=1 ;;
        -h|--help)    usage 0 ;;
        *) echo "未知参数：${1}（-h 看用法）" >&2; exit 2 ;;
    esac
    shift
done

# --password 的值若没写、或误把下一个选项吃成口令，都会变成「看似传了、其实卡在交互」，
# 所以在这里就拦掉，别等到连服务器时才发现。
if [ "$PASSWORD_FROM_CLI" = 1 ]; then
    [ -n "$DEPLOY_PASSWORD" ] || { echo "✗ --password 不能为空（-h 看用法）" >&2; exit 2; }
    case "$DEPLOY_PASSWORD" in
        --build|--no-verify|--dry-run|-n|--prune|--prune=*|-y|--yes|-h|--help|--password*|--require-migrations)
            echo "✗ --password 后面像是漏了口令，却把 $DEPLOY_PASSWORD 当成了口令" >&2; exit 2 ;;
    esac
fi

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
DIST_DIR=${DIST_DIR:-$(cd "$SCRIPT_DIR/.." && pwd)/frontend/dist}

say()  { printf '\033[1m%s\033[0m\n' "$*"; }
warn() { printf '\033[33m%s\033[0m\n' "$*" >&2; }
die()  { printf '\033[31m✗ %s\033[0m\n' "$*" >&2; exit 1; }

# ---------- 0. 前置检查 ----------

[ -d "$DIST_DIR" ] || die "找不到构建产物：${DIST_DIR}（先跑 npm run build，或加 --build）"

if [ "$DO_BUILD" = 1 ]; then
    say "== 构建 =="
    ( cd "$DIST_DIR/.." && npm run build )
    [ -f "$DIST_DIR/index.html" ] || die "构建后仍没有 index.html，构建失败了"
fi

[ -f "$DIST_DIR/index.html" ] || die "$DIST_DIR 里没有 index.html，产物不完整"

if [ -z "$DEPLOY_HOST" ]; then
    warn "⚠️  DEPLOY_HOST 为空，进入本地模式：DEPLOY_PATH=$DEPLOY_PATH 被当作本机目录"
    warn "    这只用于自测脚本逻辑，不是真正的发版。"
fi

if [ -n "$DEPLOY_HOST" ] && ! command -v ssh >/dev/null 2>&1; then
    die "本机没有 ssh"
fi

# 产物自洽性：index.html 引用的每个本地资源都必须存在。
# 抓的是「忘了重新 build 就发版」——壳引用了已被删掉的旧 hash 资源，发上去就是白屏。
say "== 校验产物自洽性 =="
MISSING=0
while IFS= read -r ref; do
    [ -n "$ref" ] || continue
    if [ ! -f "$DIST_DIR$ref" ]; then
        echo "  缺失：$ref"
        MISSING=$((MISSING + 1))
    fi
done <<EOF
$(grep -oE '/assets/[A-Za-z0-9._-]+\.(js|css)' "$DIST_DIR/index.html" | sort -u)
EOF
[ "$MISSING" -eq 0 ] || die "index.html 引用了 $MISSING 个不存在的本地文件，产物是坏的（重新 build）"
echo "  index.html 引用的资源全部存在"

# ---------- 1. 连接方式与传输方式 ----------

umask 022

# --- ssh：连接复用，密码只输一次 ---
# 不启用 ControlMaster 时，探测 / 上传 / 清理 / 自检会各开一条连接，
# 密码认证下要连输四五次（rsync 自己那条也会再问一次）。复用之后只有第一条要输。
SSH_CTL="${TMPDIR:-/tmp}/weichuan-deploy-$$"
SSH_OPTS=(-o ConnectTimeout=10
          -o ControlMaster=auto
          -o "ControlPath=$SSH_CTL"
          -o ControlPersist=180)
if [ -n "$DEPLOY_SSH_OPTS" ]; then
    # 故意不加引号：允许一次传多个参数，如 DEPLOY_SSH_OPTS="-o StrictHostKeyChecking=accept-new"
    # shellcheck disable=SC2206
    SSH_OPTS+=($DEPLOY_SSH_OPTS)
fi

# ---------- 非交互口令：把 ssh 的密码提示变成「不问人」 ----------
# ssh 的口令只从 tty / askpass 读，管道喂不进去。两条通道：
#   ① OpenSSH >= 8.4：SSH_ASKPASS_REQUIRE=force + 一个只打印环境变量的小程序。
#      口令全程只在环境变量里，不写文件、不进 argv、不进日志；所有 ssh / rsync
#      子进程都继承，配合 ControlMaster 一次认证、整程免输。
#   ② 老版本且装了 sshpass：生成一个 ssh wrapper，用 SSHPASS 环境变量喂口令。
# 两者都不具备时明确报错——绝不能静默退化成交互提示，让人以为脚本卡死了。
SSH_BIN=ssh
ASKPASS_FILE=""
cleanup_askpass() {
    if [ -n "$ASKPASS_FILE" ]; then rm -f "$ASKPASS_FILE" || true; fi
    return 0
}
trap cleanup_askpass EXIT

if [ -n "$DEPLOY_PASSWORD" ]; then
    if [ -z "$DEPLOY_HOST" ]; then
        warn "⚠️  本地模式（DEPLOY_HOST 为空）用不到 ssh 口令，已忽略"
        DEPLOY_PASSWORD=""
    else
        say "== 非交互口令 =="
        SSH_VER=$(ssh -V 2>&1 | sed -n 's/.*OpenSSH_\([0-9][0-9]*\)\.\([0-9][0-9]*\).*/\1 \2/p' || true)
        SSH_MAJOR=${SSH_VER%% *}
        SSH_MINOR=${SSH_VER##* }
        case "$SSH_MAJOR" in ''|*[!0-9]*) SSH_MAJOR=0; SSH_MINOR=0 ;; esac
        case "$SSH_MINOR" in ''|*[!0-9]*) SSH_MINOR=0 ;; esac

        if [ "$SSH_MAJOR" -gt 8 ] || { [ "$SSH_MAJOR" -eq 8 ] && [ "$SSH_MINOR" -ge 4 ]; }; then
            ASKPASS_FILE="${TMPDIR:-/tmp}/weichuan-askpass-$$"
            # 文件里只有「读环境变量」这一行，没有口令本身；700 只让它可执行。
            printf '#!/bin/sh\nprintf "%%s\\n" "$WEICHUAN_DEPLOY_PASSWORD"\n' > "$ASKPASS_FILE"
            chmod 700 "$ASKPASS_FILE"
            export WEICHUAN_DEPLOY_PASSWORD="$DEPLOY_PASSWORD"
            export SSH_ASKPASS="$ASKPASS_FILE"
            export SSH_ASKPASS_REQUIRE=force
            echo "  方式：SSH_ASKPASS（OpenSSH ${SSH_MAJOR}.${SSH_MINOR}，零额外依赖）"
        elif command -v sshpass >/dev/null 2>&1; then
            ASKPASS_FILE="${TMPDIR:-/tmp}/weichuan-sshwrap-$$"
            printf '#!/bin/sh\nexec sshpass -e ssh "$@"\n' > "$ASKPASS_FILE"
            chmod 700 "$ASKPASS_FILE"
            export SSHPASS="$DEPLOY_PASSWORD"
            SSH_BIN="$ASKPASS_FILE"
            echo "  方式：sshpass（OpenSSH ${SSH_MAJOR}.${SSH_MINOR} < 8.4）"
        else
            die "本机 OpenSSH ${SSH_MAJOR}.${SSH_MINOR} 不支持 SSH_ASKPASS_REQUIRE（需 >= 8.4），也没装 sshpass，无法非交互输入口令。
    三选一：① 升级 OpenSSH；② 装 sshpass（macOS: brew install hudochenkov/sshpass/sshpass；Linux: apt/yum install sshpass）；
    ③ ssh-copy-id $DEPLOY_HOST 配免密，之后不带口令直接发版。"
        fi
        # 口令错时别让 ssh 连问三次（每次都要走一遍 askpass），一次就够。
        SSH_OPTS+=(-o NumberOfPasswordPrompts=1)
        if [ "$PASSWORD_FROM_CLI" = 1 ]; then
            warn "  ⚠️  口令来自命令行参数，会留在 shell history / ps。下次改用 DEPLOY_PASSWORD=... 环境变量。"
        fi
    fi
fi

if [ -n "$DEPLOY_HOST" ]; then
    SSH_CMD=("$SSH_BIN" -p "$DEPLOY_PORT" "${SSH_OPTS[@]}")
    remote() { "${SSH_CMD[@]}" "$DEPLOY_HOST" "$@"; }
    TARGET="$DEPLOY_HOST:$DEPLOY_PATH"
    # rsync 的 -e 只收一个字符串，这里把数组拼回去。
    # sshpass 通道里 $SSH_BIN 是临时 wrapper 的绝对路径（由本脚本生成，不含空格/口令）。
    SSH_STR="$SSH_BIN -p $DEPLOY_PORT"
    for _o in "${SSH_OPTS[@]}"; do SSH_STR="$SSH_STR $_o"; done
    RSYNC_E=(-e "$SSH_STR")
else
    remote() { sh -c "$*"; }
    TARGET="$DEPLOY_PATH"
    RSYNC_E=()
fi

# --- 传输方式：优先 rsync（增量），远端没有就降级 tar（系统必带） ---
# 宝塔用的 CentOS 最小镜像常常不带 rsync，直接跑 rsync 会得到
#   bash: rsync: command not found / rsync error: unexpected end of file
# 这不该逼用户先上服务器装包，所以自动降级。
# 想强制指定就设 DEPLOY_TRANSFER=rsync|tar（自测 tar 分支时也用这个）。
TRANSFER=${DEPLOY_TRANSFER:-}
case "$TRANSFER" in
    ''|rsync|tar) ;;
    *) die "DEPLOY_TRANSFER 只能是 rsync 或 tar，收到：$TRANSFER" ;;
esac

if [ -n "$DEPLOY_HOST" ]; then
    say "== 检查连接 =="
    # 一条连接同时完成「连通性 + rsync 探测」：ssh 自身失败返回 255，
    # 远端找不到 rsync 返回 1，正好区分得开。
    rc=0
    remote 'command -v rsync >/dev/null 2>&1' || rc=$?
    if [ "$rc" -eq 255 ]; then
        if [ -n "$DEPLOY_PASSWORD" ]; then
            die "ssh 连不上 ${DEPLOY_HOST}（端口 ${DEPLOY_PORT}）；网络正常的话多半是口令不对（口令不会被打印）"
        fi
        die "ssh 连不上 ${DEPLOY_HOST}（端口 ${DEPLOY_PORT}）"
    fi
    # 这条连接会建立 ControlMaster 的 socket。紧接着的下一条若抢在 socket
    # 就绪之前发起，会退化成直连、再问一次密码（实测确实多问了一次），
    # 所以这里等 socket 出现再往下走。
    for _i in 1 2 3 4 5 6 7 8 9 10; do
        [ -S "$SSH_CTL" ] && break
        sleep 0.1
    done

    if [ -z "$TRANSFER" ]; then
        if [ "$rc" -eq 0 ]; then
            TRANSFER=rsync
        else
            TRANSFER=tar
        fi
    fi
    if [ "$TRANSFER" = rsync ]; then
        echo "  传输方式：rsync（增量）"
    else
        echo "  远端没有 rsync，传输方式：tar（全量约 $(du -sh "$DIST_DIR" | cut -f1)）"
        echo "  想以后走增量：ssh 上去 yum install -y rsync（或 apt install -y rsync），只装一次"
    fi
fi
TRANSFER=${TRANSFER:-rsync}

# ---------- 目标目录自检 ----------
#
# 防的是「DEPLOY_PATH 写错」：上传前会 mkdir -p，路径错了就在别处悄悄建一棵新树、
# 传满 2.6MB，而 nginx 永远不会去读它。这种事故 dry-run 完全看不出来
# （dry-run 不碰服务器），只有发完自检报「壳没换」才会发现——那时已经白传一次。
say "== 目标目录 =="
LOCAL_SHELL=$(grep -oE 'index-[A-Za-z0-9_-]+[.]js' "$DIST_DIR/index.html" | head -1)
# 三个信息（在不在 / 几个条目 / 当前壳）用一条连接取回来：每条 ssh 都是
# 一次跨境往返，拆成三条既慢又可能多问密码。
# 正则里用 [.] 代替 \. ——这串命令要穿过 sh -c 和 ssh 两层，少一层转义少一个坑。
DIR_INFO=$(remote "if [ -d '$DEPLOY_PATH' ]; then
        echo DIR_OK
        ls -1 '$DEPLOY_PATH' | wc -l
        grep -ohE 'index-[A-Za-z0-9_-]+[.]js' '$DEPLOY_PATH/index.html' 2>/dev/null | head -1
    else
        echo DIR_MISSING
    fi" || true)
DIR_STATE=$(printf '%s\n' "$DIR_INFO" | sed -n 1p)
REMOTE_ENTRIES=$(printf '%s\n' "$DIR_INFO" | sed -n 2p | tr -d ' ')
REMOTE_SHELL=$(printf '%s\n' "$DIR_INFO" | sed -n 3p)

if [ "$DIR_STATE" = "DIR_OK" ]; then
    echo "  $DEPLOY_PATH 存在（$REMOTE_ENTRIES 个条目）"
    if [ -n "$REMOTE_SHELL" ]; then
        echo "  线上当前壳：$REMOTE_SHELL"
    else
        warn "  ⚠️  这个目录里没有 index.html —— 不像站点根目录，确认路径对吗"
    fi
else
    warn "  ⚠️  $DEPLOY_PATH 不存在，脚本会新建它"
    warn "      路径写错的话，2.6MB 会传到 nginx 永远不会读的地方。"
    if [ "$DRY_RUN" != 1 ] && [ "$ASSUME_YES" != 1 ]; then
        printf '确认就是 %s ？输入 yes 继续：' "$DEPLOY_PATH"
        read -r ANSWER
        [ "$ANSWER" = "yes" ] || die "已取消（用 DEPLOY_PATH=... 指定正确路径）"
    fi
fi
echo "  本地待发壳：${LOCAL_SHELL}"

# ---------- 1.5 迁移清单完整性（防「只换产物，漏同步 migrations/」） ----------
#
# 0035 / 0038 两次线上事故形态一样：升级只替换了产物，migrations/ 目录停在旧版本。
# 后端启动的清单守门会打 [MIGRATE][FATAL] 部署包不完整，但**迁移失败刻意不终止启动**，
# 故障于是藏到运行期：写库报 Unknown column，用户只看到一句「保存失败」。
# 前端发版是升级链路的最后一棒，用一次远端命令把两份文件名清单比一次就够了。
# 注意：这是**文件名**比对，不查 schema_migrations、不代替 ./server migrate；
# 清单齐了但列没加上，还得看启动日志 / 手工跑迁移子命令。
say "== 迁移清单完整性 =="

MIGRATIONS_SKIP=0
# ${VAR-__AUTO__} 用单横杠：完全没设才推导；DEPLOY_MIGRATIONS_DIR= 显式置空 = 跳过。
case "${DEPLOY_MIGRATIONS_DIR-__AUTO__}" in
    __AUTO__)
        # 默认约定：站点目录（DEPLOY_PATH 的父目录）下，后端二进制与 migrations/ 同级。
        _SITE_DIR=$(dirname "${DEPLOY_PATH%/}")
        MIG_DIR="${_SITE_DIR}/migrations"
        ;;
    '') MIGRATIONS_SKIP=1; MIG_DIR='' ;;
    *)  MIG_DIR=${DEPLOY_MIGRATIONS_DIR} ;;
esac

LOCAL_MIGRATIONS_DIR="${SCRIPT_DIR}/../backend/migrations"

migrations_fix_hint() {
    if [ -n "${DEPLOY_HOST}" ]; then
        echo "    rsync -av backend/migrations/ ${DEPLOY_HOST}:${MIG_DIR}/"
        echo "    ssh ${DEPLOY_HOST} 'cd $(dirname "${MIG_DIR}") && ./server migrate'"
    else
        echo "    mkdir -p ${MIG_DIR}/"
        echo "    cp -a backend/migrations/. ${MIG_DIR}/"
        echo "    (cd $(dirname "${MIG_DIR}") && ./server migrate)"
    fi
    echo "    不必重启服务；完整排查见 backend/README.md「升级必做」。"
}

if [ "${MIGRATIONS_SKIP}" = 1 ]; then
    warn "  ⚠️  已显式跳过（DEPLOY_MIGRATIONS_DIR= 为空）；严格模式同样不会拦"
elif [ ! -d "${LOCAL_MIGRATIONS_DIR}" ]; then
    warn "  ⚠️  本地找不到 ${LOCAL_MIGRATIONS_DIR}，无法比对（发版包不含后端源码？）"
    warn "      迁移由别的流水线部署时，可用 DEPLOY_MIGRATIONS_DIR= 显式跳过。"
    if [ "${REQUIRE_MIGRATIONS}" = 1 ]; then die "严格模式：迁移清单无法校验，拒绝发版"; fi
else
    LOCAL_MIG_LIST=$(cd "${LOCAL_MIGRATIONS_DIR}" && ls -1 2>/dev/null | grep '[.]sql$' | sort) || LOCAL_MIG_LIST=''
    LOCAL_MIG_COUNT=$(printf '%s\n' "${LOCAL_MIG_LIST}" | grep -c . || true)
    if [ "${LOCAL_MIG_COUNT}" -eq 0 ]; then
        warn "  ⚠️  ${LOCAL_MIGRATIONS_DIR} 里没有 .sql 文件，无法比对"
        if [ "${REQUIRE_MIGRATIONS}" = 1 ]; then die "严格模式：迁移清单无法校验，拒绝发版"; fi
    else
        # 远端只回「状态行 + *.sql 文件名」，别把 ls 的报错、目录路径混进来。
        # 首行注释是给离线自检用的探针标记，远端 sh 会当注释忽略。
        MIG_RC=0
        MIG_INFO=$(remote "# __WC_MIG_PROBE__
if [ -d '${MIG_DIR}' ]; then
    echo MIG_DIR_OK
    (cd '${MIG_DIR}' && ls -1 *.sql 2>/dev/null) || true
else
    echo MIG_DIR_MISSING
fi") || MIG_RC=$?
        MIG_STATE=$(printf '%s\n' "${MIG_INFO}" | sed -n 1p)
        case "${MIG_STATE}" in
            MIG_DIR_OK)
                REMOTE_MIG_LIST=$(printf '%s\n' "${MIG_INFO}" | sed -n '2,$p' | grep '[.]sql$' | sort) || REMOTE_MIG_LIST=''
                MIG_DIR_EXISTS=1 ;;
            MIG_DIR_MISSING)
                REMOTE_MIG_LIST=''
                MIG_DIR_EXISTS=0 ;;
            *)
                # ssh 断了、路径没权限、命令被截断都会走到这里。此时**不能**
                # 把 38 个文件全报成「缺失」——那是假警报，会逼人去修根本没坏的东西。
                REMOTE_MIG_LIST=''
                MIG_DIR_EXISTS=-1 ;;
        esac

        MISSING_MIG=''
        while IFS= read -r _f; do
            [ -n "${_f}" ] || continue
            if ! printf '%s\n' "${REMOTE_MIG_LIST}" | grep -Fqx -- "${_f}"; then
                # 换行只插在两项之间、末尾不留：命令替换会吞掉尾部换行，
                # 写成 $(printf '%s%s\n' ...) 会把 N 个文件名粘成一整行。
                if [ -n "${MISSING_MIG}" ]; then MISSING_MIG="${MISSING_MIG}
"; fi
                MISSING_MIG="${MISSING_MIG}${_f}"
            fi
        done <<EOF
${LOCAL_MIG_LIST}
EOF
        MISSING_MIG_COUNT=$(printf '%s\n' "${MISSING_MIG}" | grep -c . || true)

        if [ "${MIG_DIR_EXISTS}" -lt 0 ]; then
            warn "  ⚠️  读不到远端迁移目录 ${MIG_DIR}（远端命令返回 ${MIG_RC}），本次无法比对"
            warn "      确认路径：DEPLOY_MIGRATIONS_DIR=<后端二进制同级>/migrations"
            if [ "${REQUIRE_MIGRATIONS}" = 1 ]; then die "严格模式：迁移清单无法校验，拒绝发版"; fi
        elif [ "${MISSING_MIG_COUNT}" -eq 0 ]; then
            echo "  ✓ 远端 migrations 清单一致（${LOCAL_MIG_COUNT} 个 .sql）"
        else
            if [ "${MIG_DIR_EXISTS}" = 0 ]; then
                warn "  ✗ 远端 migrations 目录不存在：${MIG_DIR}"
            else
                warn "  ✗ 远端 migrations 缺 ${MISSING_MIG_COUNT} 个文件（本地共 ${LOCAL_MIG_COUNT} 个）"
            fi
            printf '%s\n' "${MISSING_MIG}" | sed 's/^/      - /'
            warn "  这是 0035 / 0038 两次线上事故的形态：[MIGRATE][FATAL] 部署包不完整，"
            warn "  但迁移失败不终止启动，运行期才报 Unknown column，用户只看到「保存失败」。"
            warn "  修复："
            migrations_fix_hint
            if [ "${REQUIRE_MIGRATIONS}" = 1 ]; then die "严格模式：远端迁移清单不完整，拒绝发版"; fi
            warn "  ⚠️  默认只警告、继续发版。CI / 流水线请加 --require-migrations 变成硬门禁。"
        fi
    fi
fi

if [ "$TRANSFER" = rsync ]; then
    command -v rsync >/dev/null 2>&1 || die "本机没有 rsync（macOS 自带，Linux 用 apt/yum 装）"

    # 只用 openrsync（macOS 自带）和 GNU rsync 都支持的最小参数集。
    # --chmod / --chown 在 macOS 15+ 的 openrsync 上直接报 unrecognized option，
    # 所以默认不带；权限靠 umask 保证（022 → 目录 755、文件 644，nginx 能读）。
    #
    # -t（保留时间戳）不能省：rsync 的「快速判断」比较的是**大小 + mtime**。
    # 不带 -t 时目标端 mtime 每次都是新的，于是每次发版都会全量重传一遍；
    # 带上之后未改动的文件直接被跳过，只传真正变化的（跨地域上传时差别很明显）。
    RSYNC_ARGS=(-rltz)

    if [ -n "$DEPLOY_CHOWN" ]; then
        # GNU rsync 3.1+ 才有 --chown；先探测再决定，避免发版发到一半报错
        if ! rsync --chown=0:0 --version >/dev/null 2>&1; then
            die "本机 rsync 不支持 --chown（macOS 自带的是 openrsync）。
    要么别设 DEPLOY_CHOWN（文件属主会是 ssh 登录用户，644 权限下 nginx 照样能读），
    要么 brew install rsync 后确保 PATH 里是 GNU rsync。"
        fi
        RSYNC_ARGS+=(--chown="$DEPLOY_CHOWN")
    fi

    [ "$DRY_RUN" = 1 ] && RSYNC_ARGS+=(--dry-run --itemize-changes)

    # macOS 的 .DS_Store 会被 Vite 一起留在 dist 里；传上去只会让人以为「怎么多了个文件」。
    # ._* 是 AppleDouble（在部分移动硬盘/网络盘上生成），同样没意义。
    EXCLUDES=(--exclude '.DS_Store' --exclude '._*')
fi

# ---------- 2. 先传 assets（新文件先就位，旧壳用不到它们，无害） ----------

say "== 1/2 上传 assets（不删除旧文件）=="
[ -d "$DIST_DIR/assets" ] || die "产物里没有 assets/ 目录，不像 Vite 输出"

# macOS 的 bsdtar 会把文件扩展属性写进 pax 头（LIBARCHIVE.xattr.com.apple.provenance），
# 远端 GNU tar 解包时每条打一行 "Ignoring unknown extended header keyword"——
# 70 个文件就是 70 行噪音，真正的错误反而被淹掉。--no-xattrs 能关掉，
# 但 GNU tar 没有这个选项，所以先探测（纯本地操作，不碰远端）。
TAR_XOPT=""
if [ "$TRANSFER" = tar ] && tar --no-xattrs -cf /dev/null -C "$DIST_DIR" . >/dev/null 2>&1; then
    TAR_XOPT="--no-xattrs"
fi

if [ "$TRANSFER" = rsync ]; then
    rsync "${RSYNC_ARGS[@]}" "${EXCLUDES[@]}" ${RSYNC_E[@]+"${RSYNC_E[@]}"} \
        "$DIST_DIR/assets/" "$TARGET/assets/"
else
    if [ "$DRY_RUN" = 1 ]; then
        echo "  （dry-run：tar 上传 $(find "$DIST_DIR/assets" -type f | wc -l | tr -d ' ') 个文件）"
    else
        # COPYFILE_DISABLE=1：别让 macOS 的 bsdtar 额外塞 ._ AppleDouble 文件
        # --no-same-owner：以 root 解包时 GNU tar 默认会照搬包里的 uid（macOS 是 501），
        #   在服务器上变成一个不存在的属主。加上它之后文件归当前用户（root），
        #   644 权限下 nginx 照常能读。
        COPYFILE_DISABLE=1 tar $TAR_XOPT -C "$DIST_DIR/assets" -czf - . \
            | remote "mkdir -p '$DEPLOY_PATH/assets' && tar -xzf - --no-same-owner -C '$DEPLOY_PATH/assets'"
    fi
fi

# ---------- 3. 再换壳（这一步之后线上才切到新版本） ----------

say "== 2/2 上传其余文件（含 index.html，最后换壳）=="
if [ "$TRANSFER" = rsync ]; then
    rsync "${RSYNC_ARGS[@]}" "${EXCLUDES[@]}" ${RSYNC_E[@]+"${RSYNC_E[@]}"} \
        --exclude 'assets/' \
        "$DIST_DIR/" "$TARGET/"
else
    if [ "$DRY_RUN" = 1 ]; then
        echo "  （dry-run：tar 上传根文件）"
    else
        COPYFILE_DISABLE=1 tar $TAR_XOPT -C "$DIST_DIR" \
            --exclude='./assets' --exclude='./.DS_Store' --exclude='./._*' \
            -czf - . \
            | remote "tar -xzf - --no-same-owner -C '$DEPLOY_PATH'"
    fi
fi

# ---------- 4. 可选：清理旧 assets ----------
#
# 放在 dry-run 退出之前，这样 `--prune -n` 也能看到「会删哪些」，不必先真发一次。
if [ -n "$PRUNE_DAYS" ]; then
    say "== 清理 $PRUNE_DAYS 天前的旧 assets =="
    case "$PRUNE_DAYS" in
        ''|*[!0-9]*) die "--prune 的值必须是天数（整数），收到：$PRUNE_DAYS" ;;
    esac
    # 只列普通文件，不碰目录；路径限定在 $DEPLOY_PATH/assets 之内
    OLD_LIST=$(remote "find '$DEPLOY_PATH/assets' -maxdepth 1 -type f -mtime +$PRUNE_DAYS -print" || true)
    if [ -z "$OLD_LIST" ]; then
        echo "  没有需要清理的文件"
    else
        echo "$OLD_LIST" | sed 's/^/  /'
        COUNT=$(echo "$OLD_LIST" | wc -l | tr -d ' ')
        if [ "$DRY_RUN" = 1 ]; then
            echo "  （dry-run，未删除；共 $COUNT 个）"
        else
            printf '\033[1;31m⚠️  即将删除上面 %s 个文件，不可恢复（建议先在服务器上备份 assets/）\033[0m\n' "$COUNT"
            if [ "$ASSUME_YES" != 1 ]; then
                printf '确认删除请输入 yes：'
                read -r ANSWER
                [ "$ANSWER" = "yes" ] || { echo "已取消"; exit 1; }
            fi
            echo "$OLD_LIST" | while IFS= read -r f; do
                [ -n "$f" ] && remote "rm -f '$f'"
            done
            echo "  已删除 $COUNT 个"
        fi
    fi
fi

if [ "$DRY_RUN" = 1 ]; then
    say "（dry-run，什么都没改）"
    exit 0
fi

# ---------- 5. 自检 ----------

if [ "$DO_VERIFY" = 1 ] && [ -n "$DEPLOY_URL" ]; then
    say "== 自检 =="
    command -v curl >/dev/null 2>&1 || { warn "本机没有 curl，跳过自检"; exit 0; }

    LOCAL_HASH=$(grep -oE 'index-[A-Za-z0-9_-]+\.js' "$DIST_DIR/index.html" | head -1)
    # 带随机串，绕开 CDN 边缘缓存与浏览器缓存，确保看到的是源站当前内容
    REMOTE_HTML=$(curl -sS -m 20 "$DEPLOY_URL/?cb=$$$(date +%s)") || die "拉取 $DEPLOY_URL 失败"
    REMOTE_HASH=$(printf '%s' "$REMOTE_HTML" | grep -oE 'index-[A-Za-z0-9_-]+\.js' | head -1)

    if [ -z "$REMOTE_HASH" ]; then
        warn "✗ 线上首页里没找到 index-*.js，可能取到的是错误页"
        exit 1
    fi
    if [ "$LOCAL_HASH" = "$REMOTE_HASH" ]; then
        echo "  ✓ 壳已切换：$REMOTE_HASH"
    else
        warn "✗ 壳没换：本地 $LOCAL_HASH / 线上 $REMOTE_HASH"
        warn "  常见原因：① 传错目录 ② CDN 边缘还在发旧 HTML（等 60s 或清缓存）③ 本地是旧产物"
        exit 1
    fi

    # 逐个确认线上首页引用的资源都能取到（这是「白屏」的另一个成因）
    BAD=0
    for ref in $(printf '%s' "$REMOTE_HTML" | grep -oE '/assets/[A-Za-z0-9._-]+\.(js|css)' | sort -u); do
        CODE=$(curl -s -o /dev/null -m 20 -w '%{http_code}' "$DEPLOY_URL$ref")
        if [ "$CODE" != "200" ]; then
            echo "  ✗ $CODE  $ref"
            BAD=$((BAD + 1))
        fi
    done
    if [ "$BAD" -eq 0 ]; then
        echo "  ✓ 首页引用的资源全部可访问"
    else
        die "$BAD 个资源取不到，线上会白屏"
    fi
fi

say "发版完成"
