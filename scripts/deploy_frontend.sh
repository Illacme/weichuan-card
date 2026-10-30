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
#   ./scripts/deploy_frontend.sh -n                  # 只看要做什么（dry-run）
#   DEPLOY_HOST=root@其它服务器 ./scripts/deploy_frontend.sh   # 覆盖到别的服务器
#   DEPLOY_HOST= ./scripts/deploy_frontend.sh -n     # 显式置空 = 本地自测模式（不连服务器）
#
# 环境变量：
#   DEPLOY_HOST     ssh 目标，默认 root@123.56.154.49（见 DEFAULT_DEPLOY_HOST）。
#                   留空（DEPLOY_HOST=）= 本地模式（DEPLOY_PATH 当本地目录，仅用于自测）；
#                   设了就覆盖默认值，例如发到临时服务器。
#   DEPLOY_PORT     ssh 端口，默认 22
#   DEPLOY_PATH     远端站点目录，默认 /www/wwwroot/weichuan.ziyuanxingqiu.cn/frontend
#   DEPLOY_URL      发版后自检用的地址，默认 https://weichuan.ziyuanxingqiu.cn；留空跳过自检
#   DEPLOY_CHOWN    可选，如 www:www。以 root 发版时建议设置，否则文件属主是 root
#   DEPLOY_SSH_OPTS 可选，追加 ssh 参数
#   DEPLOY_TRANSFER 可选，rsync（增量，默认）/ tar（全量）。远端没装 rsync 会自动用 tar
#   DIST_DIR        可选，默认 ../frontend/dist
#
# 选项：
#   -n, --dry-run   只打印要做的事，不改任何文件
#   --build         先跑 npm run build 再发
#   --no-verify     跳过发版后的自检
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

DRY_RUN=0
DO_BUILD=0
DO_VERIFY=1
PRUNE_DAYS=""
ASSUME_YES=0

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
        --prune)      PRUNE_DAYS=7 ;;
        --prune=*)    PRUNE_DAYS=${1#*=} ;;
        -y|--yes)     ASSUME_YES=1 ;;
        -h|--help)    usage 0 ;;
        *) echo "未知参数：$1（-h 看用法）" >&2; exit 2 ;;
    esac
    shift
done

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
DIST_DIR=${DIST_DIR:-$(cd "$SCRIPT_DIR/.." && pwd)/frontend/dist}

say()  { printf '\033[1m%s\033[0m\n' "$*"; }
warn() { printf '\033[33m%s\033[0m\n' "$*" >&2; }
die()  { printf '\033[31m✗ %s\033[0m\n' "$*" >&2; exit 1; }

# ---------- 0. 前置检查 ----------

[ -d "$DIST_DIR" ] || die "找不到构建产物：$DIST_DIR（先跑 npm run build，或加 --build）"

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

if [ -n "$DEPLOY_HOST" ]; then
    SSH_CMD=(ssh -p "$DEPLOY_PORT" "${SSH_OPTS[@]}")
    remote() { "${SSH_CMD[@]}" "$DEPLOY_HOST" "$@"; }
    TARGET="$DEPLOY_HOST:$DEPLOY_PATH"
    # rsync 的 -e 只收一个字符串，这里把数组拼回去
    SSH_STR="ssh -p $DEPLOY_PORT"
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
        die "ssh 连不上 $DEPLOY_HOST（端口 $DEPLOY_PORT）"
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
echo "  本地待发壳：$LOCAL_SHELL"

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
