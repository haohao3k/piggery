#!/bin/sh
# written by piggery setup notify add herdr
# A herdr notification for each piggery notice.
# One JSON line arrives on stdin (id, from_label, kind, team, gate, dir, body, created_at). The fields
# reach the command below as arguments, never inside a command string. Needs jq.
command -v jq >/dev/null 2>&1 || { echo "piggery notify: jq not found" >&2; exit 1; }
line=$(cat)
title=$(printf '%s' "$line" | jq -r '"piggery: " + (if (.team // "") != "" then .team elif (.gate // "") != "" then .gate else "notice" end)')
body=$(printf '%s' "$line" | jq -r '.body // ""')
herdr notification show "$title" --body "$body" --sound done
