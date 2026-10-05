#!/usr/bin/env bash
# timeline.sh FILE [FROM_N [TO_N]]: one line per hook/event of a probe capture, deltas and tool.definition left out:
# n, ms since the first line, kind, name, session (last 6), and the one field that matters (status, role+finish+parent, part type).
jq -r --argjson a "${2:-0}" --argjson b "${3:-1000000}" -s '
  (.[0].t) as $t0 | .[] | select(.n>=$a and .n<=$b and .name!="message.part.delta" and .name!="tool.definition" and .name!="plugin.added" and .kind!="hook-after")
  | [.n, (.t-$t0), .kind, .name,
     ((.event.properties.sessionID // .input.sessionID // .ctx.sessionID // "-")|.[-6:]),
     (.event.properties.status.type // (.event.properties.info|if .==null then null else "\(.role // "session") \(.id[-6:])\(if .parentID then " parent=" + .parentID[-6:] else "" end)\(if .finish then " finish=" + .finish else "" end)\(if .error then " error=" + .error.name else "" end)" end) // (.event.properties.part|if .==null then null else "\(.type)\(if .tool then " " + .tool else "" end)\(if .state.status then " " + .state.status else "" end)" end) // (.event.properties.error.name) // .event.properties.reply // "")]
  | @tsv' "$1"
