# -*- coding: utf-8 -*-
"""防失真措施信息图：生成 PPT 可直接插入的两张 16:9 高清 PNG
  - anti-spoof-cards.png   : 8 条措施卡片图（按 4 组 × 2 卡布局）
  - anti-spoof-table.png   : 8 条措施对照表（措施 | 防什么 | 怎么证明）
用法: python gen-antispoof-charts.py --out <输出目录>
依赖: matplotlib（可 pip install --target <dir> matplotlib；运行时设 PYTHONPATH=<dir>）
"""
import argparse
import os

import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
from matplotlib.patches import FancyBboxPatch

plt.rcParams["font.sans-serif"] = ["Microsoft YaHei", "SimHei"]
plt.rcParams["axes.unicode_minus"] = False

INK = "#24262b"        # 正文深色
MUT = "#8a8f98"        # 次要文字
CARD_BG = "#f7f5fa"    # 卡片底
CARD_ED = "#ded8e6"    # 卡片边
GROUP_COLORS = ["#6e627a", "#7d7188", "#8b7f96", "#998da2"]  # 四组由深到浅的紫灰

# 8 条措施: (编号, 标题, 防什么, 怎么证明)
ITEMS = [
    ("1", "专业工具监考", "人工数错、掐表不准", "hey + pprof 自动记录，每笔有原始文件"),
    ("3", "大样本反复考", "一次发挥失常导致失真", "读路径 133 万次、延迟采样 60 万次"),
    ("2", "两套考卷分开", "被拦的请求误算成成功", "严格配置考规则，放宽配置考能力"),
    ("8", "主动写明前提", "实验室上限当成生产承诺", "标注空库/本机/放宽，建议按 1/5~1/10 折算"),
    ("4", "先查监考工具", "工具毛病嫁祸给服务端", "修复端口耗尽等 3 处客户端干扰"),
    ("5", "异常值当警报", "一个异常高分拉高平均", "重测剔除 43,602 异常，5 实例收窄"),
    ("6", "服务端留底", "报错被刷掉看不见", "全程日志落盘，0 字节无 panic"),
    ("7", "一键可复现", "数字死无对证", "bench-all.ps1 一键重跑，原始 CSV 全保留"),
]
GROUPS = ["A 把数字弄准", "B 别把账算错", "C 排除场外干扰", "D 留好重考入口"]


def _card(ax, x0, y0, w, h, num, title, prevent, prove, color):
    ax.add_patch(FancyBboxPatch(
        (x0, y0), w, h, boxstyle="round,pad=0.005,rounding_size=0.010",
        facecolor=CARD_BG, edgecolor=CARD_ED, linewidth=1.2, zorder=2))
    # 编号徽章
    bw, bh = 0.046, 0.046
    bx, by = x0 + 0.014, y0 + h - 0.052
    ax.add_patch(FancyBboxPatch(
        (bx, by), bw, bh, boxstyle="round,pad=0.002,rounding_size=0.008",
        facecolor=color, edgecolor="none", zorder=3))
    ax.text(bx + bw / 2, by + bh / 2, num, ha="center", va="center",
            fontsize=25, color="white", fontweight="bold", zorder=4)
    # 标题
    ax.text(x0 + 0.072, y0 + h - 0.026, title, ha="left", va="center",
            fontsize=27, color=INK, fontweight="bold", zorder=4)
    # 防什么 / 怎么证明
    ax.text(x0 + 0.072, y0 + 0.082, "防：" + prevent, ha="left", va="center",
            fontsize=21, color=color, zorder=4)
    ax.text(x0 + 0.072, y0 + 0.030, "怎么证明：" + prove, ha="left", va="center",
            fontsize=16, color=MUT, zorder=4)


def draw_cards(out):
    fig, ax = plt.subplots(figsize=(19.2, 10.8), dpi=100)
    ax.set_xlim(0, 1); ax.set_ylim(0, 1); ax.axis("off")
    fig.patch.set_facecolor("white")

    ax.text(0.03, 0.955, "压测防失真 · 8 道防作弊关卡", ha="left", va="center",
            fontsize=42, color=INK, fontweight="bold")
    ax.text(0.03, 0.912, "每条措施都在回答：这分数是蒙对的、抄来的，还是老师放水的？",
            ha="left", va="center", fontsize=22, color=MUT)

    for gi, gname in enumerate(GROUPS):
        y0 = 0.095 + gi * 0.195            # 行矩形底
        h = 0.175
        color = GROUP_COLORS[gi]
        # 组标签：左侧色带 + 竖排组名
        ax.add_patch(FancyBboxPatch(
            (0.045, y0 + 0.010), 0.013, h - 0.020,
            boxstyle="round,pad=0.001,rounding_size=0.004",
            facecolor=color, edgecolor="none", zorder=2))
        ax.text(0.068, y0 + h / 2, gname, rotation=90, ha="center", va="center",
                fontsize=21, color=color, fontweight="bold", zorder=3)
        # 两张卡片
        for ci in range(2):
            num, title, prevent, prove = ITEMS[gi * 2 + ci]
            x0 = 0.15 + ci * 0.425
            w = 0.395
            _card(ax, x0, y0, w, h, num, title, prevent, prove, color)

    ax.text(0.03, 0.032, "数据来源：docs/07-性能测试报告.md §5 · 2026-08-13 实测压测（hey / pprof / 逐秒采样）",
            ha="left", va="center", fontsize=16, color=MUT)
    fig.savefig(os.path.join(out, "anti-spoof-cards.png"), dpi=100, facecolor="white", bbox_inches="tight")
    plt.close(fig)


# (措施, 防什么, 怎么证明) —— 每条 ≤ ~16 字，适配 PPT 大字号
ROWS = [
    ("1 · 专业工具监考", "人工数错、掐表不准", "hey + pprof 自动记录留档"),
    ("2 · 两套考卷分开", "被拦的请求误算成成功", "严格考规则，放宽考能力"),
    ("3 · 大样本反复考", "一次发挥失常致失真", "读 133 万次、延迟 60 万次"),
    ("4 · 先查监考工具", "工具毛病嫁祸服务端", "修复 3 处客户端干扰"),
    ("5 · 异常值当警报", "异常高分拉高平均", "重测剔除 43,602 异常"),
    ("6 · 服务端留底", "报错被刷掉看不见", "全程日志 0 字节无 panic"),
    ("7 · 一键可复现", "数字死无对证", "bench-all.ps1 重跑可复现"),
    ("8 · 主动写明前提", "上限当成生产承诺", "标注空库/本机/放宽口径"),
]


def draw_table(out):
    fig, ax = plt.subplots(figsize=(19.2, 10.8), dpi=100)
    ax.set_xlim(0, 1); ax.set_ylim(0, 1); ax.axis("off")
    fig.patch.set_facecolor("white")

    ax.text(0.03, 0.955, "压测防失真 · 8 条措施对照表", ha="left", va="center",
            fontsize=40, color=INK, fontweight="bold")
    ax.text(0.03, 0.912, "怎么判断这些压测数字可不可信？下面每条都回答了「防什么」和「怎么证明」。",
            ha="left", va="center", fontsize=22, color=MUT)

    # 表头与列布局
    cols = [(0.035, 0.29, "措施"), (0.335, 0.30, "防止什么失真"), (0.645, 0.325, "怎么证明")]
    head_y = 0.845
    ax.text(0.03, head_y, "8 条防失真措施", ha="left", va="center",
            fontsize=24, color=INK, fontweight="bold")
    for x0, w, label in cols:
        ax.add_patch(FancyBboxPatch(
            (x0, head_y - 0.052), w, 0.052,
            boxstyle="round,pad=0.002,rounding_size=0.006",
            facecolor="#6e627a", edgecolor="none", zorder=2))
        ax.text(x0 + w / 2, head_y - 0.026, label, ha="center", va="center",
                fontsize=21, color="white", fontweight="bold", zorder=3)

    row_h = 0.084
    for i, (name, prev, prove) in enumerate(ROWS):
        y_top = 0.775 - i * 0.092           # 行顶 y
        if i % 2 == 0:
            ax.add_patch(FancyBboxPatch(
                (0.035, y_top - row_h), 0.935, row_h,
                boxstyle="round,pad=0.002,rounding_size=0.008",
                facecolor=CARD_BG, edgecolor="none", zorder=1))
        ax.text(0.035 + 0.006, y_top - row_h / 2, name, ha="left", va="center",
                fontsize=21, color=INK, fontweight="bold", zorder=3)
        ax.text(0.335 + 0.006, y_top - row_h / 2, "防：" + prev, ha="left", va="center",
                fontsize=20, color="#6e627a", zorder=3)
        ax.text(0.645 + 0.006, y_top - row_h / 2, prove, ha="left", va="center",
                fontsize=20, color=MUT, zorder=3)

    ax.text(0.03, 0.030, "数据来源：docs/07-性能测试报告.md §5 · 2026-08-13 实测压测（hey / pprof / 逐秒采样）",
            ha="left", va="center", fontsize=16, color=MUT)
    fig.savefig(os.path.join(out, "anti-spoof-table.png"), dpi=100, facecolor="white", bbox_inches="tight")
    plt.close(fig)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", required=True, help="输出目录")
    args = ap.parse_args()
    os.makedirs(args.out, exist_ok=True)
    draw_cards(args.out)
    draw_table(args.out)
    print("done:", os.path.join(args.out, "anti-spoof-cards.png"), os.path.join(args.out, "anti-spoof-table.png"))


if __name__ == "__main__":
    main()
