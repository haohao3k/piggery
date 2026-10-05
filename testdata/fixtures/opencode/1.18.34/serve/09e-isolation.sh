#!/bin/bash
# Q6: what else of the user's setup leaks into a worker and how to turn it off (no model turns): ~/.claude skills and CLAUDE.md
# (seen in the system prompt in 08), the providers list (opencode's own hosted provider is "connected"), network connections of an idle serve.
. "$(dirname "$0")/lib.sh"; cd "$S"; O=09e-isolation
exec > >(redact > $O.txt) 2>&1
probe() { # probe NAME : serve with the current env
  serve_start $O-$1 --port 0; api POST /session '{"title":"p"}' >/dev/null; sleep 4
  echo "## $1 (start_ms=$START_MS)"
  echo "  GET /skill: $(api GET /skill | python3 -c 'import json,sys;d=json.load(sys.stdin);print(len(d),"skills; locations:",sorted({("~/"+x["location"].split("/home/user/")[1].split("/")[0]+"/"+x["location"].split("/home/user/")[1].split("/")[1]) if "/home/user/" in x["location"] else x["location"] for x in d}))')"
  echo "  GET /config/providers ids: $(api GET /config/providers | python3 -c 'import json,sys;d=json.load(sys.stdin);print([p["id"] for p in d["providers"]])')"
  echo "  GET /provider connected: $(api GET /provider | python3 -c 'import json,sys;print(json.load(sys.stdin)["connected"])')"
  echo "  network connections of serve (lsof -i, remote ends only):"; lsof -nP -i -a -p $SPID 2>/dev/null | awk 'NR>1 && $9 ~ /->/ {print "    " $8, $9}' | sed 's/127.0.0.1:[0-9]*->127.0.0.1:[0-9]*/loopback/' | sort | uniq -c
  kill $SPID; wait $SPID 2>/dev/null
}
probe default
export OPENCODE_DISABLE_CLAUDE_CODE=1; probe disable-claude-code; unset OPENCODE_DISABLE_CLAUDE_CODE
export OPENCODE_DISABLE_MODELS_FETCH=1 OPENCODE_CONFIG_CONTENT='{"enabled_providers":["hp"]}'; probe models-fetch-off-enabled-providers-hp
