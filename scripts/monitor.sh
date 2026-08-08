#!/usr/bin/env bash
# monitor.sh —— dwxComment 内存稳定性观测脚本（Linux 服务器使用）
#
# 用法:
#   bash monitor.sh <PID> <输出CSV> [pprof端口]
#   例: bash monitor.sh $(pgrep -f dwx-comment) /var/log/lc-monitor.csv
#
# 说明:
#   - 每秒采样 VmRSS / VmSize / goroutines / HeapInuse 追加写入 CSV
#   - pprof 端口默认 6060（程序内固定监听 127.0.0.1:6060）
#   - 判定泄漏: heap_inuse_kb 在 GC 后持续抬升不回基线、vmrss_kb 24h+ 单调爬升、goroutines 持续上涨 = 真泄漏
#     （RSS 爬到平台后稳定属于 Go 堆缓存正常现象，不是泄漏）
#   - 配合压测: hey -z 30m -c 10 -m POST ... http://127.0.0.1:8080/api/v1/comment
#   - 事后快速汇总: head -1 <CSV>; tail -1 <CSV>

PID=${1:?用法: bash monitor.sh <PID> <CSV路径> [pprof端口]}
OUT=${2:?用法: bash monitor.sh <PID> <CSV路径> [pprof端口]}
PORT=${3:-6060}

if [ ! -d "/proc/$PID" ]; then
  echo "找不到进程 PID=$PID" >&2
  exit 1
fi

mkdir -p "$(dirname "$OUT")"
[ -f "$OUT" ] || printf 'ts,vmrss_kb,vmsize_kb,goroutines,heap_inuse_kb\n' > "$OUT"
echo "PID=$PID 观测中（pprof 端口 $PORT），Ctrl-C 停止，结果追加到 $OUT"

trap 'echo "已停止，结果见 $OUT"' EXIT

while kill -0 "$PID" 2>/dev/null; do
  ts=$(date '+%Y-%m-%d %H:%M:%S')
  read -r vmrss vmsize < <(awk '/^VmRSS:|^VmSize:/{print $2}' "/proc/$PID/status" | paste -sd' ')
  vmrss=${vmrss:-0}; vmsize=${vmsize:-0}

  goroutines=0; heap_inuse=0
  if curl -sf --max-time 2 "http://127.0.0.1:$PORT/debug/pprof/goroutine?debug=1" > /tmp/lc-g.txt 2>/dev/null; then
    goroutines=$(grep -m1 -o 'total [0-9]*' /tmp/lc-g.txt | awk '{print $2}')
  fi
  if curl -sf --max-time 2 "http://127.0.0.1:$PORT/debug/pprof/heap?debug=1" > /tmp/lc-h.txt 2>/dev/null; then
    # debug=1 输出为 "# HeapInuse = 4251648"，数值为第 4 列
    heap_inuse=$(grep -m1 '^# HeapInuse' /tmp/lc-h.txt | awk '{print $4}')
  fi

  printf '%s,%s,%s,%s,%s\n' "$ts" "$vmrss" "$vmsize" "${goroutines:-0}" "${heap_inuse:-0}" >> "$OUT"
  sleep 1
done

echo "进程已退出，观测结束"
