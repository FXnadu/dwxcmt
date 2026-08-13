#!/usr/bin/env bash
# clean-avatars.sh —— 清理 data/avatars 下历史遗留的孤儿头像缓存文件（Linux 服务器使用）
#
# 背景: v3.3 起头像缓存键从「评论 ID」改为「邮箱 md5」。
#   旧格式（孤儿）: {评论ID}.jpg/png/gif/webp 与 {评论ID}.none
#   新格式（保留）: {邮箱md5}.jpg/png/gif/webp 与 {邮箱md5}.none（32 位小写 hex）
# 删除旧文件不影响功能：下次访问对应评论头像时会按新逻辑重新回源并缓存。
#
# 用法:
#   bash clean-avatars.sh [--dry-run] [目录]
#   --dry-run 仅列出将删除的文件，不实际删除（建议先跑一遍确认）
#   默认目录: data/avatars（相对脚本运行目录）
set -euo pipefail

DRY_RUN=false
DIR="data/avatars"
if [ "${1:-}" = "--dry-run" ]; then DRY_RUN=true; shift; fi
if [ -n "${1:-}" ]; then DIR="$1"; fi

if [ ! -d "$DIR" ]; then
  echo "目录不存在: $DIR（无需清理）"
  exit 0
fi

removed=0
for f in "$DIR"/*; do
  [ -f "$f" ] || continue
  name=$(basename "$f")
  base="${name%.*}"
  # 新格式：32 位小写 hex（邮箱 md5），保留
  if [[ "$base" =~ ^[0-9a-f]{32}$ ]]; then
    continue
  fi
  # 旧格式：纯数字（评论 ID 命名），孤儿，删除
  if [[ "$base" =~ ^[0-9]+$ ]]; then
    if [ "$DRY_RUN" = true ]; then
      echo "[dry-run] 将删除: $f"
    else
      rm -f -- "$f"
      echo "已删除: $f"
    fi
    removed=$((removed + 1))
  else
    echo "跳过（无法识别，保守保留）: $f"
  fi
done

if [ "$DRY_RUN" = true ]; then
  echo "dry-run 完成，共 $removed 个旧格式文件将被删除"
else
  echo "清理完成，共删除 $removed 个旧格式文件"
fi
