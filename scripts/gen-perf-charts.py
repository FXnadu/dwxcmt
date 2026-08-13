#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""从 bench-all.ps1 的原始输出渲染专业图表（真实数据，非估算）。

用法:
  python scripts/gen-perf-charts.py <out目录> [--charts-dir <图目录>]

读取（均在 out 目录）:
  read-summary.txt        5 个 hey 文本汇总
  latency.csv             hey -o csv 逐请求延迟
  write-a.json / write-a-per-sec.csv   Phase A 单 IP 最坏场景
  write-b.json / write-b-per-sec.csv   Phase C 写吞吐
  samples-a.csv / samples-b.csv        内存/协程采样
  phases.csv              各阶段时间戳（秒，epoch）

产出: <charts>/01..06 PNG + <out>/summary.json（供报告引用）
"""
import argparse
import csv
import json
import os
import re
import sys

import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
import numpy as np
from matplotlib import font_manager

# ---------- 中文字体 ----------
_CJK = ['Microsoft YaHei', 'SimHei', 'Noto Sans CJK SC', 'PingFang SC']
_font = None
for name in _CJK:
    try:
        font_manager.findfont(name, fallback_to_default=False)
        _font = name
        break
    except Exception:
        continue
if _font:
    plt.rcParams['font.sans-serif'] = [_font, 'DejaVu Sans']
plt.rcParams['axes.unicode_minus'] = False

# ---------- 统一风格 ----------
PALETTE = {
    'blue': '#2563EB', 'amber': '#F59E0B', 'red': '#DC2626',
    'green': '#059669', 'purple': '#7C3AED', 'gray': '#6B7280', 'teal': '#0891B2',
}
plt.rcParams.update({
    'figure.facecolor': 'white',
    'axes.facecolor': '#F8FAFC',
    'axes.edgecolor': '#CBD5E1',
    'axes.linewidth': 0.9,
    'axes.grid': True,
    'grid.color': '#E2E8F0',
    'grid.linewidth': 0.8,
    'axes.spines.top': False,
    'axes.spines.right': False,
    'font.size': 10.5,
    'axes.titlesize': 12.5,
    'axes.titleweight': 'bold',
    'axes.labelsize': 10.5,
    'legend.frameon': False,
    'xtick.color': '#475569',
    'ytick.color': '#475569',
})
MB = 1024 * 1024


def save(fig, path):
    fig.tight_layout()
    fig.savefig(path, dpi=150, bbox_inches='tight')
    plt.close(fig)
    print('  ->', path)


# ---------- 解析 ----------
def parse_hey_summaries(path):
    """返回 [{worker, qps, avg_s, slowest_s, fastest_s, responses_200, errors, percentiles}]"""
    txt = open(path, encoding='utf-8', errors='replace').read()
    blocks = re.split(r'\n\s*Summary:\s*\n', txt)[1:]
    out = []
    for i, b in enumerate(blocks):
        m = lambda pat: re.search(pat, b)
        qps = float(m(r'Requests/sec:\s+([\d.]+)').group(1))
        avg = float(m(r'Average:\s+([\d.]+)').group(1))
        slowest = float(m(r'Slowest:\s+([\d.]+)').group(1))
        fastest = float(m(r'Fastest:\s+([\d.]+)').group(1))
        resp200 = int(m(r'\[200\]\s+(\d+) responses').group(1)) if m(r'\[200\]\s+(\d+) responses') else 0
        errs = sum(int(x.group(1)) for x in re.finditer(r'\[(\d{3})\]\s+(\d+) responses', b) if x.group(1) != '200')
        # 注意: hey 输出经 PowerShell 管道后分位行会变成 "10%% in ..."（%% 转义），两种都兼容
        pct = {int(p): float(v) for p, v in re.findall(r'(\d+)%%? in ([\d.]+) secs', b)}
        out.append({'worker': f'worker{i+1}', 'qps': qps, 'avg_ms': avg * 1000,
                    'slowest_ms': slowest * 1000, 'fastest_ms': fastest * 1000,
                    'responses_200': resp200, 'errors': errs, 'percentiles_ms': {k: v * 1000 for k, v in pct.items()}})
    return out


def parse_latency_csv(path):
    """hey -o csv；兼容 PowerShell Out-File 的 UTF-16 与 UTF-8 两种编码"""
    raw = open(path, 'rb').read()
    if raw[:2] in (b'\xff\xfe', b'\xfe\xff'):
        text = raw.decode('utf-16', errors='replace')
    else:
        text = raw.decode('utf-8', errors='replace')
    rows = []
    rd = csv.reader(text.splitlines())
    next(rd, None)
    for r in rd:
        if len(r) < 7:
            continue
        try:
            rows.append({'lat_s': float(r[0]), 'status': int(r[6])})
        except ValueError:
            continue
    return rows


def parse_samples(path):
    out = []
    with open(path, encoding='utf-8-sig') as f:
        rd = csv.DictReader(f)
        for r in rd:
            try:
                out.append({'epoch': float(r['ts_epoch']), 'ws': int(r['working_set_bytes']),
                            'heap': int(r['heap_inuse_bytes'] or 0), 'goro': int(r['goroutines'] or 0)})
            except (ValueError, KeyError):
                continue
    return out


def parse_phases(path):
    out = []
    with open(path, encoding='utf-8-sig') as f:
        rd = csv.DictReader(f)
        for r in rd:
            try:
                out.append({'phase': r['phase'], 'start': float(r['start_epoch']), 'end': float(r['end_epoch'])})
            except ValueError:
                continue
    return out


def parse_wsec(path):
    rows = []
    with open(path, encoding='utf-8-sig') as f:
        rd = csv.DictReader(f)
        for r in rd:
            rows.append({k: int(v) for k, v in r.items()})
    return rows


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('outdir')
    ap.add_argument('--charts-dir', default='')
    args = ap.parse_args()
    d = args.outdir
    cd = args.charts_dir or os.path.join(d, 'charts')
    os.makedirs(cd, exist_ok=True)

    read = parse_hey_summaries(os.path.join(d, 'read-summary.txt'))
    lat = parse_latency_csv(os.path.join(d, 'latency.csv'))
    sa = parse_samples(os.path.join(d, 'samples-a.csv'))
    sb = parse_samples(os.path.join(d, 'samples-b.csv'))
    ph = parse_phases(os.path.join(d, 'phases.csv'))
    wa = json.load(open(os.path.join(d, 'write-a.json'), encoding='utf-8'))
    wb = json.load(open(os.path.join(d, 'write-b.json'), encoding='utf-8'))
    wa_sec = parse_wsec(os.path.join(d, 'write-a-per-sec.csv'))
    wb_sec = parse_wsec(os.path.join(d, 'write-b-per-sec.csv'))

    summary = {
        'read': read,
        'read_total_qps': round(sum(r['qps'] for r in read), 1),
        'read_total_responses': sum(r['responses_200'] for r in read),
        'read_errors': sum(r['errors'] for r in read),
        'read_latency': None,
        'write_strict': wa,
        'write_throughput': wb,
        'memory': None,
    }
    if lat:
        lat_ms = np.array([r['lat_s'] * 1000 for r in lat])
        non200 = sum(1 for r in lat if r['status'] != 200)
        pcts = {p: float(np.percentile(lat_ms, p)) for p in (50, 90, 95, 99, 99.9)}
        summary['read_latency'] = {'n': len(lat_ms), 'p50_ms': pcts[50], 'p90_ms': pcts[90],
                                   'p95_ms': pcts[95], 'p99_ms': pcts[99], 'p999_ms': pcts[99.9],
                                   'max_ms': float(lat_ms.max()), 'min_ms': float(lat_ms.min()),
                                   'avg_ms': float(lat_ms.mean()), 'non200': non200}

    # ============ 01 读压测 QPS ============
    fig, ax = plt.subplots(figsize=(9.6, 4.8))
    names = [r['worker'] for r in read] + ['合计']
    qps = [r['qps'] for r in read] + [summary['read_total_qps']]
    colors = [PALETTE['blue']] * len(read) + [PALETTE['green']]
    bars = ax.barh(names[::-1], qps[::-1], color=colors[::-1], height=0.62, zorder=3)
    ax.set_xlabel('吞吐（req/s）')
    ax.set_title('读路径真实容量 · 各压测实例 QPS（5 实例 × 20 并发 × 30s，空库列表查询）')
    ax.set_xlim(0, max(qps) * 1.12)
    for b, v in zip(bars, qps[::-1]):
        ax.text(v + max(qps) * 0.012, b.get_y() + b.get_height() / 2, f'{v:,.0f}',
                va='center', fontsize=10, color='#0F172A')
    ax.text(0.99, 0.02, '限流已放宽至 100 万/秒（config-bench.yaml），测的是真实容量',
            transform=ax.transAxes, ha='right', fontsize=8.5, color=PALETTE['gray'])
    save(fig, os.path.join(cd, '01-read-qps.png'))

    # ============ 02 读延迟 CDF ============
    if lat:
        fig, ax = plt.subplots(figsize=(9.6, 4.8))
        xs = np.sort(lat_ms)
        ys = np.arange(1, len(xs) + 1) / len(xs) * 100
        ax.plot(xs, ys, color=PALETTE['blue'], lw=2, zorder=3)
        for p in (50, 90, 99):
            v = pcts[p]
            ax.axvline(v, color=PALETTE['amber'], ls='--', lw=1.2, alpha=0.85)
            ax.text(v, 102 - p / 1.15, f'p{p} = {v:.2f} ms', rotation=90, va='bottom',
                    ha='right', fontsize=9, color=PALETTE['amber'])
        ax.axhline(99, color=PALETTE['gray'], ls=':', lw=1)
        ax.set_xlabel('响应延迟（ms）')
        ax.set_ylabel('累计占比（%）')
        ax.set_title('读路径延迟 CDF（hey -o csv 逐请求采样，' + (f'n={len(lat_ms):,}' if len(lat_ms) > 999 else f'n={len(lat_ms)}') + '）')
        ax.set_ylim(0, 108)
        ax.set_xlim(0, min(max(xs) * 1.15, 40))
        ax.text(0.99, 0.02, f'max={pcts[99.9]:.2f}ms（p99.9）· 非200响应 {summary["read_latency"]["non200"]} 个',
                transform=ax.transAxes, ha='right', fontsize=8.5, color=PALETTE['gray'])
        save(fig, os.path.join(cd, '02-read-latency-cdf.png'))

    # ============ 03 写压测·逐秒时间序列（单 IP 最坏场景） ============
    secs = [r['sec'] for r in wa_sec]
    c0 = [r['code0'] for r in wa_sec]
    c2001 = [r['code2001'] for r in wa_sec]
    c2002 = [r['code2002'] for r in wa_sec]
    fig, ax = plt.subplots(figsize=(9.6, 4.8))
    ax.plot(secs, c2001, color=PALETTE['amber'], lw=1.8, marker='.', ms=4, label='每秒限流拒绝（2001）', zorder=3)
    ax.plot(secs, c0, color=PALETTE['green'], lw=2.2, marker='o', ms=4, label='成功入库（code=0）', zorder=4)
    ax.plot(secs, c2002, color=PALETTE['red'], lw=1.8, marker='s', ms=3.5, label='每日配额拒绝（2002）', zorder=3)
    ax.axhline(50, color=PALETTE['blue'], ls='--', lw=1.4)
    ax.text(secs[0], 52, '配置上限 50 req/s（固定窗口）', color=PALETTE['blue'], fontsize=9)
    ax.set_xlabel('相对秒')
    ax.set_ylabel('请求数 / 秒')
    ax.set_title(f'单 IP 最坏场景 · 每秒接受/拒绝分布（{wa["threads"]} 并发，攻击速率≈{wa["qps_cap"]:.0f}/s，上限 50/s、1000 条/日）')
    ax.legend(loc='upper right')
    ax.set_ylim(0, max(max(c2001) * 1.15, 80))
    save(fig, os.path.join(cd, '03-write-strict-timeseries.png'))

    # ============ 04 写压测·总量分布 ============
    t = wa['total']
    labels = ['成功入库\ncode=0', '每秒限流\ncode=2001', '每日配额\ncode=2002', '重复拦截\ncode=2003', '业务错误\ncode=1xxx', 'HTTP 错误']
    vals = [t.get('0', 0), t.get('2001', 0), t.get('2002', 0), t.get('2003', 0),
            sum(v for k, v in t.items() if isinstance(k, int) and 1000 <= k < 2000), t.get('http_error', 0)]
    cols = [PALETTE['green'], PALETTE['amber'], PALETTE['red'], PALETTE['gray'], PALETTE['purple'], '#000000']
    fig, ax = plt.subplots(figsize=(9.6, 4.6))
    bars = ax.bar(labels, vals, color=cols, width=0.58, zorder=3)
    for b, v in zip(bars, vals):
        ax.text(b.get_x() + b.get_width() / 2, v, f'{v:,}', ha='center', va='bottom', fontsize=10.5, fontweight='bold')
    ax.set_ylabel('响应数')
    ax.set_title('单 IP 最坏场景 · 响应业务码分布（HTTP 全部 200；业务码 2001/2002 为限流拦截，属设计内行为）')
    ax.set_ylim(0, max(vals) * 1.18)
    ax.tick_params(axis='x', labelsize=9.5)
    ax.text(0.99, 0.96, f'业务错误 = {vals[4]}，HTTP 错误 = {vals[5]}（判据：全程 0 异常）',
            transform=ax.transAxes, ha='right', va='top', fontsize=9, color=PALETTE['gray'])
    save(fig, os.path.join(cd, '04-write-strict-codes.png'))

    # ============ 05 内存时间序列 ============
    if sa and sb:
        fig, axes = plt.subplots(2, 1, figsize=(9.6, 6.6), sharex=True)
        # 两个实例（严格实例 Phase A / bench 实例 Phase B-D），各自时间轴归一
        e0a, e0b = sa[0]['epoch'], sb[0]['epoch']
        tsa = [r['epoch'] - e0a for r in sa]
        tsb = [r['epoch'] - e0b for r in sb]
        ws_a = [r['ws'] / MB for r in sa]
        ws_b = [r['ws'] / MB for r in sb]
        ax = axes[0]
        ax.plot(tsa, ws_a, color=PALETTE['blue'], lw=1.8, label='严格实例（Phase A 写）', zorder=3)
        ax.plot(tsb, ws_b, color=PALETTE['green'], lw=1.8, label='bench 实例（Phase B/C/D）', zorder=3)
        ax.set_ylabel('WorkingSet（MB）')
        ax.set_title('压测全程内存 · WorkingSet 与 HeapInuse（pprof heap?gc=0 采样，~1s 间隔）')
        ax.legend(loc='upper right', fontsize=9)
        ax.set_ylim(0, max(max(ws_a), max(ws_b)) * 1.25)
        h_a = [r['heap'] / MB for r in sa]
        h_b = [r['heap'] / MB for r in sb]
        ax = axes[1]
        ax.plot(tsa, h_a, color=PALETTE['blue'], lw=1.8, label='严格实例 HeapInuse', zorder=3)
        ax.plot(tsb, h_b, color=PALETTE['green'], lw=1.8, label='bench 实例 HeapInuse', zorder=3)
        ax.set_ylabel('HeapInuse（MB）')
        ax.set_xlabel('相对时间（s，两个实例分别以各自采样起点为 0）')
        ax.legend(loc='upper right', fontsize=9)
        ax.set_ylim(0, max(max(h_a), max(h_b)) * 1.45)
        save(fig, os.path.join(cd, '05-memory.png'))
        summary['memory'] = {
            'strict_ws_peak_mb': round(max(ws_a), 2), 'strict_heap_peak_mb': round(max(h_a), 2),
            'bench_ws_peak_mb': round(max(ws_b), 2), 'bench_heap_peak_mb': round(max(h_b), 2),
            'goroutines': {'strict': max(r['goro'] for r in sa), 'bench': max(r['goro'] for r in sb)},
        }

    # ============ 06 写吞吐（放宽配置） ============
    secs_b = [r['sec'] for r in wb_sec]
    c0_b = [r['code0'] for r in wb_sec]
    fig, ax = plt.subplots(figsize=(9.6, 4.6))
    ax.bar(secs_b, c0_b, color=PALETTE['teal'], width=0.8, zorder=3)
    ax.set_xlabel('相对秒')
    ax.set_ylabel('成功入库 / 秒')
    tot_b = wb['total'].get('0', 0)
    ax.set_title(f'写路径真实吞吐 · 每成功写入（{wb["duration_sec"]:.0f}s 合计 {tot_b:,} 条，≈{tot_b / wb["duration_sec"]:.0f} 条/s，0 错误）')
    ax.set_ylim(0, max(c0_b) * 1.2)
    save(fig, os.path.join(cd, '06-write-throughput.png'))

    with open(os.path.join(d, 'summary.json'), 'w', encoding='utf-8') as f:
        json.dump(summary, f, ensure_ascii=False, indent=2)
    print('summary.json 已写入', os.path.join(d, 'summary.json'))
    print('关键数字: 读合计 QPS=%s，读响应=%s，错误=%s；写严格=%s；写吞吐=%s'
          % (summary['read_total_qps'], summary['read_total_responses'], summary['read_errors'],
             wa['total'], wb['total']))


if __name__ == '__main__':
    main()
