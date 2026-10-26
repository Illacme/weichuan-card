#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────
# 最小告警检查（零第三方依赖，四条规则，配 crontab 每分钟运行）：
#   R1 healthz    ：本地探活 GET /healthz，非 2xx / 超时即告警
#   R2 5xx        ：访问日志新增 [ACCESS-5XX] 行即告警（附请求 ID 便于排查）
#   R3 备份新鲜度 ：backups/ 最新备份文件年龄超阈值即告警
#                   （认 *.sql.gz 与 *.db.gz 两种产物；backup.sh 失败时不落新文件，
#                    本规则可兜底捕获）
#   R4 外部拨测   ：从本机之外访问对外地址（EXTERNAL_URL）：非 2xx / 耗时超阈值 /
#                   证书临近过期即告警。R1 是本机视角，看不出反代、证书、防火墙
#                   这类「本机全绿、用户全挂」的故障，R4 补的就是这一半
#
# 定位：不替代 Prometheus/Alertmanager，而是「没有监控基建也能跑起来」
# 的最小闭环；规则是纯增量，随时可平滑迁到标准监控栈。
#
# 用法：
#   scripts/alert_check.sh
#   ALERT_WEBHOOK=https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=xxx \
#     HEALTH_URL=http://127.0.0.1:8080/healthz \
#     EXTERNAL_URL=https://card.example.com/healthz \
#     LOG_FILE=/var/log/weichuan/access.log \
#     BACKUP_DIR=./backups scripts/alert_check.sh
#
# 配置（环境变量，均有默认值）：
#   HEALTH_URL       默认 http://127.0.0.1:8080/healthz
#   LOG_FILE         后端访问日志文件（AccessLog 的 stderr 收集处），
#                    默认 /var/log/weichuan/access.log
#   BACKUP_DIR       备份目录（与 backup.sh 的 BACKUP_DIR 一致），默认 ./backups
#   BACKUP_MAX_HOURS 最新备份允许的最大年龄（小时），默认 26（日备 + 2h 余量）
#   STATE_DIR        状态目录（日志偏移量 / 告警冷却时间戳），默认 /var/tmp/weichuan-alert
#   ALERT_WEBHOOK    机器人 webhook（企业微信 / 钉钉，msgtype=text 同格式；
#                    飞书格式不同需自行调整 -d 负载）；未配置时告警打到 stderr
#   ALERT_COOLDOWN   同一规则两次告警的最小间隔（秒），默认 1800（防轰炸）
#   EXTERNAL_URL     外部拨测地址（对外 https 基址 + /healthz），默认空=跳过 R4
#   EXTERNAL_MAX_MS  外部拨测耗时阈值（毫秒），默认 2000
#   CERT_MIN_DAYS    证书剩余有效期告警阈值（天），默认 14
#
# 平台：Linux（GNU coreutils）；BSD/macOS 的 stat/date 差异已在 file_mtime 内兼容。
# 依赖：bash、curl（探活 / 外部拨测 / webhook 推送）、grep/tail/wc、awk（耗时取整）、
# openssl（仅证书有效期，缺失则跳过该子项）。
# ─────────────────────────────────────────────────────────────
set -uo pipefail

HEALTH_URL="${HEALTH_URL:-http://127.0.0.1:8080/healthz}"
LOG_FILE="${LOG_FILE:-/var/log/weichuan/access.log}"
BACKUP_DIR="${BACKUP_DIR:-./backups}"
BACKUP_MAX_HOURS="${BACKUP_MAX_HOURS:-26}"
STATE_DIR="${STATE_DIR:-/var/tmp/weichuan-alert}"
ALERT_WEBHOOK="${ALERT_WEBHOOK:-}"
ALERT_COOLDOWN="${ALERT_COOLDOWN:-1800}"
EXTERNAL_URL="${EXTERNAL_URL:-}"
EXTERNAL_MAX_MS="${EXTERNAL_MAX_MS:-2000}"
CERT_MIN_DAYS="${CERT_MIN_DAYS:-14}"

command -v curl >/dev/null 2>&1 || { echo "[alert_check] 需要 curl（探活与 webhook 推送）" >&2; exit 1; }
mkdir -p "$STATE_DIR"

# ── 通知：per-rule 冷却防轰炸；webhook 不可达时降级到 stderr ──
# 用法：send_alert <规则名> <消息> [冷却秒数]
send_alert() {
  local rule="$1" msg="$2" cooldown="${3:-$ALERT_COOLDOWN}"
  local now last stamp text safe
  now="$(date +%s)"
  stamp="$STATE_DIR/cooldown_$rule"
  last=0
  [ -f "$stamp" ] && last="$(cat "$stamp" 2>/dev/null || echo 0)"
  case "$last" in ''|*[!0-9]*) last=0 ;; esac
  if [ $((now - last)) -lt "$cooldown" ]; then
    return 0
  fi
  echo "$now" > "$stamp"

  # 日志行可能含引号 / 反斜杠 / 换行，转义后截断再进 JSON
  safe="$(printf '%s' "$msg" | tr -d '\r\n' | sed 's/\\/\\\\/g; s/"/\\"/g' | cut -c1-300)"
  text="[weichuan-alert] $rule: $safe ($(date '+%F %T'))"
  if [ -n "$ALERT_WEBHOOK" ]; then
    if ! curl -fsS -m 10 -X POST "$ALERT_WEBHOOK" \
        -H 'Content-Type: application/json' \
        -d "{\"msgtype\":\"text\",\"text\":{\"content\":\"$text\"}}" >/dev/null 2>&1; then
      echo "${text}（webhook 发送失败，降级为本行输出）" >&2
    fi
  else
    echo "$text" >&2
  fi
}

# ── R1: healthz 探活 ──
check_healthz() {
  if curl -fsS -m 10 -o /dev/null "$HEALTH_URL" 2>/dev/null; then
    return 0
  fi
  send_alert healthz "GET $HEALTH_URL 探活失败（进程退出 / 端口不通 / 反代故障）"
}

# ── R2: 访问日志新增 [ACCESS-5XX] 行 ──
# 字节偏移量只扫增量，避免每次全量重扫造成重复告警；
# 日志轮转（文件变小）时偏移归零重扫，由冷却时间兜底防轰炸。
check_5xx() {
  if [ ! -f "$LOG_FILE" ]; then
    echo "[alert_check] 日志文件不存在，跳过 R2：$LOG_FILE" >&2
    return 0
  fi
  local size last new chunk sample
  size="$(wc -c < "$LOG_FILE" | tr -d ' ')"
  last=0
  [ -f "$STATE_DIR/log_offset" ] && last="$(cat "$STATE_DIR/log_offset" 2>/dev/null || echo 0)"
  case "$last" in ''|*[!0-9]*) last=0 ;; esac
  if [ "$size" -lt "$last" ]; then
    last=0
  fi
  chunk="$(tail -c "+$((last + 1))" "$LOG_FILE" 2>/dev/null)"
  echo "$size" > "$STATE_DIR/log_offset"
  new="$(printf '%s\n' "$chunk" | grep -c '\[ACCESS-5XX\]')"
  if [ "${new:-0}" -gt 0 ]; then
    sample="$(printf '%s\n' "$chunk" | grep '\[ACCESS-5XX\]' | tail -1 | cut -c1-160)"
    send_alert 5xx "访问日志新增 $new 条 [ACCESS-5XX]；最新一条：$sample"
  fi
}

# ── R3: 备份新鲜度 ──
file_mtime() {
  stat -c %Y "$1" 2>/dev/null && return 0
  stat -f %m "$1" 2>/dev/null && return 0
  perl -e 'print((stat($ARGV[0]))[9])' "$1" 2>/dev/null
}

check_backup() {
  if [ ! -d "$BACKUP_DIR" ]; then
    echo "[alert_check] 备份目录不存在，跳过 R3：$BACKUP_DIR" >&2
    return 0
  fi
  local latest m age_h
  # 两种备份产物都要认：MySQL 是 .sql.gz，SQLite 是 .db.gz（见 backup.sh 的 DB_DRIVER）。
  # 只认一种会让另一种部署的 R3 永远「没有备份文件」——告警天天响，然后被静音。
  latest="$(ls -t "$BACKUP_DIR"/weichuan-*.sql.gz "$BACKUP_DIR"/weichuan-*.db.gz 2>/dev/null | head -1 || true)"
  if [ -z "$latest" ]; then
    # 从未备份，或备份停跑且旧文件已轮转——两种情况都意味着"当前没有可用备份"
    send_alert backup "备份目录 $BACKUP_DIR 中没有备份文件（从未备份，或备份停跑且旧文件已轮转）" 21600
    return 0
  fi
  m="$(file_mtime "$latest")"
  [ -n "$m" ] || return 0
  age_h=$(( ( $(date +%s) - m ) / 3600 ))
  if [ "$age_h" -ge "$BACKUP_MAX_HOURS" ]; then
    send_alert backup "最新备份 $(basename "$latest") 已 ${age_h}h 未更新（阈值 ${BACKUP_MAX_HOURS}h），备份任务可能未运行"
  fi
}

# ── R4: 外部拨测（R1 看不到的那一半：反代 / 证书 / 防火墙）──
# 从本机 curl 127.0.0.1 永远 200，而用户侧可能是「域名没解析 / 443 没放行 /
# 证书过期 / 反代 upstream 挂了」——这些只有从对外地址访问才暴露。
# 未配 EXTERNAL_URL 时跳过（很多部署用 UptimeRobot 之类的外部服务代劳，不重复造）。
check_external() {
  if [ -z "$EXTERNAL_URL" ]; then
    echo "[alert_check] 未设置 EXTERNAL_URL，跳过 R4（建议填对外 https 地址 + /healthz）" >&2
    return 0
  fi
  local out code ms
  out="$(curl -sS -o /dev/null -m 10 -w '%{http_code} %{time_total}' "$EXTERNAL_URL" 2>/dev/null)" || out=""
  if [ -z "$out" ]; then
    # 无响应时直接返回：此时证书必然也取不到，再报一条 cert 只是重复噪音
    send_alert external "外部拨测 $EXTERNAL_URL 无响应（域名不可达 / 防火墙未放行 / 反代故障）"
    return 0
  fi
  code="${out%% *}"
  ms="$(printf '%s' "${out#* }" | awk '{printf "%d", $1 * 1000}')"
  case "$code" in
    2??) ;;
    *) send_alert external "外部拨测 $EXTERNAL_URL 返回 HTTP ${code}（期望 2xx）：反代或后端异常" ;;
  esac
  if [ -n "${ms:-}" ] && [ "$ms" -gt "$EXTERNAL_MAX_MS" ] 2>/dev/null; then
    send_alert external_slow "外部拨测 $EXTERNAL_URL 耗时 ${ms}ms > ${EXTERNAL_MAX_MS}ms：链路变慢，先查后端与带宽"
  fi

  # 证书：只有 https 才查；openssl 缺失则跳过（不因缺工具让整条规则失效）
  case "$EXTERNAL_URL" in
    https://*) ;;
    *) return 0 ;;
  esac
  command -v openssl >/dev/null 2>&1 || return 0
  local hp host secs crc
  hp="$(printf '%s' "$EXTERNAL_URL" | sed -e 's#^https://##' -e 's#/.*$##')"
  host="${hp%%:*}"
  [ "$hp" = "$host" ] && hp="$host:443"
  secs=$((CERT_MIN_DAYS * 86400))
  crc=0
  cert_probe "$hp" "$host" "$secs" || crc=$?
  case "$crc" in
    0) ;;
    2) send_alert cert "取不到 ${host} 的证书（443 未开放 / 域名不可达 / SNI 不匹配）" 21600 ;;
    *)
      if cert_probe "$hp" "$host" 0; then
        send_alert cert "${host} 的证书将在 ${CERT_MIN_DAYS} 天内过期（已配自动续期可忽略）" 21600
      else
        send_alert cert "${host} 的证书已过期：所有 https 请求失败，小程序正式版不可用"
      fi
      ;;
  esac
}

# cert_probe <host:port> <servername> <checkend_seconds>
# 退出码：0=在 N 秒后仍未过期 / 1=已过期或即将过期 / 2=没拿到证书。
# 与 preflight.sh 的 cert_check 同源（两份脚本刻意不互相 source，避免依赖目录结构）。
cert_probe() {
  local hp="$1" sn="$2" end="$3" tmp pid i rc
  tmp="${STATE_DIR}/cert-${sn}-$$"
  ( printf '' | openssl s_client -servername "$sn" -connect "$hp" ) >"$tmp" 2>/dev/null &
  pid=$!
  i=0
  while [ "$i" -lt 100 ]; do
    kill -0 "$pid" 2>/dev/null || break
    sleep 0.1
    i=$((i + 1))
  done
  kill "$pid" 2>/dev/null
  wait "$pid" 2>/dev/null
  # 与 preflight.sh 同理：拿不到证书要和「已过期」区分开
  grep -q "BEGIN CERTIFICATE" "$tmp" 2>/dev/null || { rm -f "$tmp"; return 2; }
  openssl x509 -noout -checkend "$end" <"$tmp" >/dev/null 2>&1
  rc=$?
  rm -f "$tmp"
  if [ "$rc" -ne 0 ] && [ "$end" != "0" ]; then rc=1; fi
  return $rc
}

check_healthz
check_5xx
check_backup
check_external
exit 0
