#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""写压测负载生成器：并发 POST /api/v1/comment，按响应业务码分桶统计（含逐秒时间序列）。

用途（配合 scripts/bench-all.ps1）：
  1. 抗滥用压测（--qps 限速）：单 IP 最坏场景，验证 50 rps / 1000 条/日 精确生效；
  2. 吞吐基准（--qps 0 = 不限速）：测量写路径真实容量。

判定口径（与线上语义一致）：
  - code=0    成功入库（HTTP 200）
  - code=2001 每秒限流拒绝（HTTP 200，业务码）
  - code=2002 每日配额拒绝（HTTP 200，业务码）
  - code=2003 5 分钟内重复内容拦截
  - code=1xxx 参数校验等业务错误
  - http_error 连接失败 / 5xx / 超时（这才是"HTTP 错误"）

用法:
  python scripts/stress-write.py --base http://127.0.0.1:8080 --seconds 25 --threads 32 \
      --ip 203.0.113.50 --site stress --pages 20 --qps 800 \
      --out <out>/write-a.json --csv <out>/write-a-per-sec.csv
"""
import argparse
import csv as _csv
import http.client
import json
import random
import threading
import time
from collections import Counter, defaultdict
from urllib.parse import urlparse


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--base', default='http://127.0.0.1:8080')
    ap.add_argument('--seconds', type=float, default=25)
    ap.add_argument('--threads', type=int, default=32)
    ap.add_argument('--ip', default='203.0.113.50')
    ap.add_argument('--site', default='stress')
    ap.add_argument('--pages', type=int, default=20)
    ap.add_argument('--nick', default='stress')
    ap.add_argument('--qps', type=float, default=0, help='0=不限速（最大吞吐）；>0 时全局限速到该值')
    ap.add_argument('--out', required=True, help='JSON 汇总输出路径')
    ap.add_argument('--csv', required=True, help='逐秒 CSV 输出路径')
    args = ap.parse_args()

    stop_at = time.time() + args.seconds
    start = time.time()
    per_sec = defaultdict(Counter)   # 相对秒 -> {业务码: 次数}
    total = Counter()
    http_err = Counter()
    lat_sum = 0.0
    lat_n = 0
    lock = threading.Lock()
    url = args.base.rstrip('/') + '/api/v1/comment'

    # 可选限速：每个线程每次请求后的固定间隔，总速率约等于 args.qps
    per_thread_delay = (args.threads / args.qps) if args.qps and args.qps > 0 else 0.0

    u = urlparse(url)
    host, port, path = u.hostname, u.port or (443 if u.scheme == 'https' else 80), u.path
    conn_cls = http.client.HTTPSConnection if u.scheme == 'https' else http.client.HTTPConnection

    def worker(w):
        nonlocal lat_sum, lat_n
        conn = conn_cls(host, port, timeout=10)
        while True:
            now = time.time()
            if now >= stop_at:
                try:
                    conn.close()
                except Exception:
                    pass
                return
            page = random.randint(1, args.pages)
            content = 'stress-%s-%d-%016x' % (args.nick, w, random.getrandbits(64))
            body = json.dumps({
                'pageId': 'stress-page-%d' % page,
                'site': args.site,
                'nick': '%s%d' % (args.nick, w),
                'content': content,
            }).encode('utf-8')
            headers = {
                'Content-Type': 'application/json',
                'X-Forwarded-For': args.ip,
                'User-Agent': 'stress-bench/1.0',
                'Connection': 'keep-alive',
                'Content-Length': str(len(body)),
            }
            t0 = time.time()
            code = None
            errmsg = None
            # 每线程持有一个持久连接：短连接（800/s 新建）会耗尽 Windows 临时端口
            # （TIME_WAIT 堆积），那是客户端环境的伪错误，与"0 HTTP 错误"的服务端结论无关。
            # 发送阶段失败（连接已被对端关闭）→ 请求未送达，重建连接重试一次、安全；
            # 响应阶段失败 → 请求已送达服务端，不重试（避免重复入库污染统计），计 http_error。
            for attempt in (1, 2):
                try:
                    conn.request('POST', path, body, headers)
                    break
                except (ConnectionError, OSError, http.client.HTTPException) as e:
                    try:
                        conn.close()
                    except Exception:
                        pass
                    conn = conn_cls(host, port, timeout=10)
                    if attempt == 2:
                        code = 'http_error'
                        errmsg = 'send:%s: %s' % (type(e).__name__, str(e))
            if code is None:
                try:
                    resp = conn.getresponse()
                    d = json.loads(resp.read().decode('utf-8'))
                    c = d.get('code')
                    code = c if isinstance(c, int) else 'unknown'
                except (ConnectionError, OSError, http.client.HTTPException, ValueError) as e:
                    code = 'http_error'
                    errmsg = 'recv:%s: %s' % (type(e).__name__, str(e))
                    try:
                        conn.close()
                    except Exception:
                        pass
                    conn = conn_cls(host, port, timeout=10)
            dt = time.time() - t0
            sec = int(now - start)
            with lock:
                per_sec[sec][code] += 1
                total[code] += 1
                if errmsg is not None:
                    http_err[errmsg] += 1
                lat_sum += dt
                lat_n += 1
            if per_thread_delay:
                time.sleep(per_thread_delay)

    threads = [threading.Thread(target=worker, args=(i,), daemon=True) for i in range(args.threads)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()

    summary = {
        'duration_sec': round(time.time() - start, 3),
        'threads': args.threads,
        'ip': args.ip,
        'site': args.site,
        'qps_cap': args.qps,
        'total': dict(total),
        'avg_latency_ms': round(lat_sum / lat_n * 1000, 3) if lat_n else None,
        'http_error_samples': dict(list(http_err.items())[:5]),
    }
    with open(args.out, 'w', encoding='utf-8') as f:
        json.dump(summary, f, ensure_ascii=False, indent=2)

    with open(args.csv, 'w', encoding='utf-8', newline='') as f:
        w = _csv.writer(f)
        w.writerow(['sec', 'code0', 'code2001', 'code2002', 'code2003', 'code1xxx', 'http_error'])
        for sec in sorted(per_sec):
            c = per_sec[sec]
            w.writerow([
                sec,
                c.get(0, 0),
                c.get(2001, 0),
                c.get(2002, 0),
                c.get(2003, 0),
                sum(v for k, v in c.items() if isinstance(k, int) and 1000 <= k < 2000),
                c.get('http_error', 0),
            ])
    print(json.dumps(summary, ensure_ascii=False))


if __name__ == '__main__':
    main()
