#!/bin/sh
# written by piggery setup notify add desktop
# A desktop notification for each piggery notice: terminal-notifier (when installed) or osascript on
# macOS, notify-send on Linux.
# One JSON line arrives on stdin (id, from_label, kind, team, gate, dir, body, created_at). The fields
# reach the command below as arguments, never inside a command string. Needs jq.
command -v jq >/dev/null 2>&1 || { echo "piggery notify: jq not found" >&2; exit 1; }
line=$(cat)
title=$(printf '%s' "$line" | jq -r '"piggery: " + (if (.team // "") != "" then .team elif (.gate // "") != "" then .gate else "notice" end)')
body=$(printf '%s' "$line" | jq -r '.body // ""')
if [ "$(uname)" = Darwin ]; then
  # terminal-notifier first; when it is missing or exits non-zero, osascript (by its absolute path:
  # the daemon's PATH may not be your shell's)
  if command -v terminal-notifier >/dev/null 2>&1; then
    terminal-notifier -title "$title" -message "$body" && exit 0
  fi
  /usr/bin/osascript -e 'on run argv' -e 'display notification (item 2 of argv) with title (item 1 of argv)' -e 'end run' "$title" "$body"
else
  notify-send -- "$title" "$body"
fi
