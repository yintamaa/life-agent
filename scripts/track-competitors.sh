#!/usr/bin/env bash
# 竞品跟踪：拉取 docs/research/competitors.json 里每个上游的最新提交，
# 报告"距上次复核落后多少提交、节奏多快、改了哪些我们关心的方向"。
#
#   scripts/track-competitors.sh            # 全部
#   scripts/track-competitors.sh persona    # 只看一个
#
# 只拉提交历史（blobless、shallow-since 复核日期前一天），不检出代码、不执行任何竞品代码。
# 缓存目录：${LIFE_AGENT_RESEARCH_CACHE:-~/.cache/life-agent-research}
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
list="$root/docs/research/competitors.json"
cache="${LIFE_AGENT_RESEARCH_CACHE:-$HOME/.cache/life-agent-research}"
only="${1:-}"
mkdir -p "$cache"
command -v jq >/dev/null || { echo "需要 jq" >&2; exit 1; }

keywords="$(jq -r '.keywords' "$list")"
n="$(jq '.competitors | length' "$list")"
i=0
while [ "$i" -lt "$n" ]; do
  name="$(jq -r ".competitors[$i].name" "$list")"
  repo="$(jq -r ".competitors[$i].repo" "$list")"
  rev="$(jq -r ".competitors[$i].reviewed_commit" "$list")"
  since="$(jq -r ".competitors[$i].reviewed_at" "$list")"
  i=$((i + 1))
  if [ -n "$only" ] && [ "$only" != "$name" ]; then continue; fi

  dir="$cache/$name"
  # shallow-since 往前多留一天，保证复核时的提交在历史里
  from="$(date -j -v-1d -f %Y-%m-%d "$since" +%Y-%m-%d 2>/dev/null || date -d "$since -1 day" +%Y-%m-%d)"
  if [ -d "$dir/.git" ]; then
    git -C "$dir" fetch -q --filter=blob:none --shallow-since="$from" origin HEAD 2>/dev/null || true
    head="FETCH_HEAD"
  else
    rm -rf "$dir"
    GIT_TERMINAL_PROMPT=0 git clone -q --filter=blob:none --no-checkout --shallow-since="$from" "$repo" "$dir"
    head="HEAD"
  fi
  latest="$(git -C "$dir" log -1 --format='%h %cs' "$head")"
  echo "== $name  ($repo)"
  echo "   最新：$latest    上次复核：${rev:-无} @ $since"
  if [ -n "$rev" ] && git -C "$dir" cat-file -e "$rev^{commit}" 2>/dev/null; then
    behind="$(git -C "$dir" rev-list --count "$rev..$head")"
    days="$(( ( $(date +%s) - $(date -j -f %Y-%m-%d "$since" +%s 2>/dev/null || date -d "$since" +%s) ) / 86400 ))"
    [ "$days" -lt 1 ] && days=1
    echo "   落后：$behind 个提交（约 $((behind / days)) 个/天）"
    hits="$(git -C "$dir" log --format='%s' "$rev..$head" | grep -i -E "$keywords" || true)"
    echo "   相关提交：$(printf '%s' "$hits" | grep -c . || true) 个，其中 feat："
    { printf '%s\n' "$hits" | grep -E '^feat' | head -15 | sed 's/^/     · /'; } || true
    if [ "$behind" -gt 200 ]; then echo "   ⚠ 建议重新复核（落后超过 200 个提交）"; fi
  else
    echo "   （找不到上次复核的提交：可能从未做过源码分析，或复核日期早于缓存的历史）"
  fi
  echo
done
