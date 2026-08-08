#!/usr/bin/env bash
# stress.sh —— dwxComment 高并发压测脚本（hey + 内存采样）
#
# 目标：验证 1G 内存环境下长时间高压运行的稳定性（内存不泄漏、不 OOM、接口不雪崩）
#
# 用法：
#   bash scripts/stress.sh
#   可选环境变量（均有默认值，按需覆盖）：
#     BASE           服务地址（默认 http://127.0.0.1:8080）
#     PPROF          pprof 观测端口（默认 http://127.0.0.1:6060）
#     SITE           压测使用独立站点名，隔离生产数据（默认 stress）
#     READ_WORKERS   读压测 hey 实例数（默认 5）
#     READ_CONCURRENCY 每实例并发（默认 20）
#     READ_SECONDS   读压测时长秒（默认 30）
#     READ_PAGES     读压测页面数（默认 20）
#     WRITE_WORKERS  写压测并发 worker 数（默认 8）
#     WRITE_SECONDS  写压测时长秒（默认 20）
#     WRITE_IP_POOL  写压测伪造 IP 池大小（默认 500，每 IP 每日限 20 条评论，
#                    池大小 ×20 即压测期间总写入上限，需 ≥ 预计请求量）
#     MIXED_SECONDS  读写混合时长秒，0 表示跳过（默认 0）
#
# 为什么写压测用 curl 而读压测用 hey：
#   - 全局限流 5 req/s/IP：hey 单实例只能固定一个 X-Forwarded-For，会被限死在 5 rps；
#     读压测用 N 个 hey 实例各持不同 IP 即可叠加吞吐。
#   - 写压测还有去重规则（同 IP+pageId+content 5 分钟内算重复）和每日 20 条/IP 上限，
#     hey 无法为每个请求随机化 body，故写路径用并发 curl 逐请求随机 IP/pageId/content。
#   - 服务将 127.0.0.1 视为可信代理，压测与生产 Nginx 场景一致，XFF 会被采纳。
#
# 输出目录：/tmp/lc-stress-<时间戳>/（samples.csv、各阶段报告、heap 快照）
# 结果判定：见脚本末尾打印的“泄漏判定”小结。

set -u
BASE=${BASE:-http://127.0.0.1:8080}
PPROF=${PPROF:-http://127.0.0.1:6060}
SITE=${SITE:-stress}
READ_WORKERS=${READ_WORKERS:-5}
READ_CONCURRENCY=${READ_CONCURRENCY:-20}
READ_SECONDS=${READ_SECONDS:-30}
READ_PAGES=${READ_PAGES:-20}
WRITE_WORKERS=${WRITE_WORKERS:-8}
WRITE_SECONDS=${WRITE_SECONDS:-20}
WRITE_IP_POOL=${WRITE_IP_POOL:-500}
MIXED_SECONDS=${MIXED_SECONDS:-0}

OUT="/tmp/lc-stress-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$OUT"
SAMPLER_PID=""

log() { echo "[$(date '+%H:%M:%S')] $*"; }
die() { log "错误: $*"; exit 1; }

# ---------- 预检 ----------
command -v hey >/dev/null 2>&1 || die "未安装 hey（go install github.com/rakyll/hey@latest）"
command -v curl >/dev/null 2>&1 || die "未安装 curl"

log "预检: $BASE/api/v1/health"
curl -sf --max-time 5 "$BASE/api/v1/health" >/dev/null || die "服务不可达，请先启动 light-comment"
log "预检: pprof $PPROF/debug/pprof/goroutine"
curl -sf --max-time 5 "$PPROF/debug/pprof/goroutine?debug=1" >/dev/null || die "pprof 不可达（应为 127.0.0.1:6060）"

PID=$(pgrep -f light-comment | head -1)
[ -n "${PID:-}" ] || die "找不到 light-comment 进程"
log "观测进程 PID=$PID"

# ---------- 工具函数 ----------
# 采样进程 VmRSS(KB) 与堆占用（heap 接口会先触发一次 GC，得到可回收后的基线值，最适合判泄漏；
# 注意 # HeapInuse 单位是字节）
sample_mem() {
  [ -r "/proc/$PID/status" ] || return
  awk '/^VmRSS:/{print $2}' "/proc/$PID/status"
}
heap_inuse_bytes() {
  curl -sf --max-time 3 "$PPROF/debug/pprof/heap?debug=1" | grep -m1 '^# HeapInuse' | awk '{print $3}'
}

start_sampler() {
  : > "$OUT/samples.csv"
  echo "ts,vmrss_kb,heap_inuse_bytes" > "$OUT/samples.csv"
  (
    while kill -0 "$PID" 2>/dev/null; do
      printf '%s,%s,%s\n' "$(date '+%H:%M:%S')" "$(sample_mem)" "$(heap_inuse_bytes)" >> "$OUT/samples.csv"
      sleep 2
    done
  ) &
  SAMPLER_PID=$!
}
stop_sampler() { [ -n "$SAMPLER_PID" ] && kill "$SAMPLER_PID" 2>/dev/null; SAMPLER_PID=""; }
trap 'stop_sampler; log "已中断，中间结果见 $OUT"' INT TERM

# ---------- 阶段一：读压测（hey，多实例各持不同 IP） ----------
run_read_phase() {
  log "阶段一 读压测: ${READ_WORKERS} 实例 × ${READ_CONCURRENCY} 并发 × ${READ_SECONDS}s，${READ_PAGES} 个页面"
  local urls=()
  for i in $(seq 1 "$READ_PAGES"); do
    urls+=("$BASE/api/v1/comments?site=$SITE&pageId=stress-page-$i&pageSize=20")
  done
  local pids=()
  for i in $(seq 1 "$READ_WORKERS"); do
    hey -c "$READ_CONCURRENCY" -z "${READ_SECONDS}s" \
      -H "X-Forwarded-For: 10.0.0.$i" \
      "${urls[@]}" > "$OUT/read-worker-$i.txt" 2>&1 &
    pids+=($!)
  done
  wait "${pids[@]}"
  log "阶段一 完成，各实例 QPS 与状态码分布:"
  grep -hE 'Requests/sec:|Status code distribution:|\[[0-9]{3}\]' "$OUT"/read-worker-*.txt | sed 's/^/  /' || true
}

# ---------- 阶段二：写压测（并发 curl，逐请求随机 IP/pageId/content） ----------
run_write_phase() {
  log "阶段二 写压测: ${WRITE_WORKERS} worker × ${WRITE_SECONDS}s，IP 池 ${WRITE_IP_POOL}（注意每日 20 条/IP 上限）"
  local ips=()
  local i
  for i in $(seq 1 "$WRITE_IP_POOL"); do
    ips+=("10.$((RANDOM % 256)).$((RANDOM % 256)).$((RANDOM % 253 + 2))")
  done
  : > "$OUT/write-summary.txt"

  write_worker() {
    local w=$1 deadline=$2
    local ok=0 rate=0 fail=0 other=0
    while [ "$(date +%s)" -lt "$deadline" ]; do
      local ip=${ips[$((RANDOM % WRITE_IP_POOL))]}
      local page=$((RANDOM % READ_PAGES + 1))
      local nick="st$w$((RANDOM % 10000))"
      local content="stress-$w-$RANDOM-$RANDOM-$RANDOM"
      local body="{\"pageId\":\"stress-page-$page\",\"site\":\"$SITE\",\"nick\":\"$nick\",\"content\":\"$content\"}"
      local code
      code=$(curl -s -o /dev/null -w '%{http_code}' -m 10 -X POST \
        -H "Content-Type: application/json" \
        -H "X-Forwarded-For: $ip" \
        -d "$body" "$BASE/api/v1/comment")
      case "$code" in
        200) ok=$((ok + 1)) ;;
        429) rate=$((rate + 1)) ;;
        4*)  other=$((other + 1)) ;;
        *)   fail=$((fail + 1)) ;;
      esac
    done
    echo "worker$w ok=$ok rate429=$rate 4xx=$other fail=$fail" >> "$OUT/write-summary.txt"
  }

  local deadline=$(( $(date +%s) + WRITE_SECONDS ))
  local w
  for w in $(seq 1 "$WRITE_WORKERS"); do
    write_worker "$w" "$deadline" &
  done
  wait
  log "阶段二 完成，各 worker 结果:"
  sed 's/^/  /' "$OUT/write-summary.txt"
  awk -F'[ =]' '{o+=$3; r+=$5} END{printf "  合计 ok=%d rate429=%d（限流即正常，说明在打真实限流/排队逻辑）\n", o, r}' "$OUT/write-summary.txt"
}

# ---------- 阶段三：读写混合（可选） ----------
run_mixed_phase() {
  log "阶段三 混合压测: ${MIXED_SECONDS}s"
  local urls=()
  local i
  for i in $(seq 1 5); do
    urls+=("$BASE/api/v1/comments?site=$SITE&pageId=stress-page-$i&pageSize=20")
  done
  hey -c 10 -z "${MIXED_SECONDS}s" -H "X-Forwarded-For: 10.0.1.1" "${urls[@]}" > "$OUT/mixed-read.txt" 2>&1 &
  local p1=$!

  local deadline=$(( $(date +%s) + MIXED_SECONDS ))
  local ips=()
  for i in $(seq 1 30); do ips+=("10.2.$((RANDOM % 256)).$((RANDOM % 253 + 2))"); done
  mixed_worker() {
    local w=$1 dl=$2
    while [ "$(date +%s)" -lt "$dl" ]; do
      local ip=${ips[$((RANDOM % 30))]}
      local body="{\"pageId\":\"stress-page-$((RANDOM % 5 + 1))\",\"site\":\"$SITE\",\"nick\":\"mx$w\",\"content\":\"mix-$w-$RANDOM\"}"
      curl -s -o /dev/null -m 10 -X POST \
        -H "Content-Type: application/json" -H "X-Forwarded-For: $ip" \
        -d "$body" "$BASE/api/v1/comment"
    done
  }
  for w in 1 2 3; do
    mixed_worker "$w" "$deadline" &
  done
  wait "$p1"
  wait
  log "阶段三 完成"
}

# ---------- 主流程 ----------
log "== LightComment 压测开始，输出目录: $OUT =="

# 压测前基线（pprof 堆接口会先 GC，取的是可回收后基线）
BEFORE_HEAP=$(heap_inuse_bytes)
BEFORE_RSS=$(sample_mem)
log "压测前基线: RSS=${BEFORE_RSS:-?}KB HeapInuse=${BEFORE_HEAP:-?}B"
curl -sf --max-time 5 "$PPROF/debug/pprof/heap" > "$OUT/heap-before.prof"

start_sampler
run_read_phase
run_write_phase
[ "$MIXED_SECONDS" -gt 0 ] && run_mixed_phase
stop_sampler

# 压测后采样（同样先 GC 再取堆，与基线同口径）
AFTER_HEAP=$(heap_inuse_bytes)
AFTER_RSS=$(sample_mem)
curl -sf --max-time 5 "$PPROF/debug/pprof/heap" > "$OUT/heap-after.prof"

log "== 压测结束 =="
log "压测后: RSS=${AFTER_RSS:-?}KB HeapInuse=${AFTER_HEAP:-?}B"
log "内存采样曲线见 $OUT/samples.csv（首/末行）:"
head -2 "$OUT/samples.csv" | sed 's/^/  /'
tail -2 "$OUT/samples.csv" | sed 's/^/  /'

# ---------- 泄漏判定小结 ----------
MAX_RSS=$(awk -F, 'NR>1 && $2>m{m=$2} END{print m+0}' "$OUT/samples.csv")
echo
echo "======================================================"
echo " 泄漏判定（判据）"
echo "======================================================"
echo "  1) 采样峰值 RSS : ${MAX_RSS}KB（1G 机器建议 VmRSS 峰值 < 600MB，即 < 614400KB）"
echo "  2) HeapInuse(字节) 基线->压后: ${BEFORE_HEAP:-?} -> ${AFTER_HEAP:-?} B"
echo "     （GC 后可回收口径。若压后 >> 基线且长时间不回、24h+ 单调爬升 = 真泄漏）"
echo "  3) 各 worker rate429=0 反而可疑：说明没有真正打穿限流，并发没压上去"
echo "  4) 对比堆快照定位泄漏点:"
echo "     go tool pprof -top $OUT/heap-before.prof"
echo "     go tool pprof -top $OUT/heap-after.prof"
echo "  5) 观察 OOM: dmesg -T | grep -i oom   （配合 systemd MemoryMax 时看是否被 OOM 杀）"
echo "======================================================"
