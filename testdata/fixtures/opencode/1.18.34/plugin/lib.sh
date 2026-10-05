# Shared helpers for the opencode capture capture (a): plugin surface. Source it. Needs /tmp/oc (a copy of /tmp/oc/xdg + env.sh, see README).
# The key never reaches a capture: probe.js redacts it, and `redact` filters anything else.
S=${S:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)}
source /tmp/oc/env.sh
unset OPENCODE_SERVER_PASSWORD OPENCODE_SERVER_USERNAME OPENCODE_CONFIG OPENCODE_CONFIG_DIR OPENCODE_CONFIG_CONTENT OPENCODE_PERMISSION
export PROBE_CTL=/tmp/oc/ctl.jsonl PROBE_FLAGS=/tmp/oc/flags.json
W=/tmp/oc/work                 # the project directory (a git repo)
PIDS=/tmp/oc/pids              # every pid this capture started, one per line
CFG=$XDG_CONFIG_HOME/opencode
mkdir -p "$W" ; touch "$PIDS"
[ -d "$W/.git" ] || { git -C "$W" init -q && git -C "$W" commit -q --allow-empty -m init; }
redact() { python3 -c 'import os,sys
k=os.environ.get("HP_KEY","")
d=sys.stdin.read()
sys.stdout.write(d.replace(k,"<HP_KEY>") if k else d)'; }
now_ms() { python3 -c 'import time;print(int(time.time()*1000))'; }
# serve_start PORT: opencode serve in $W; sets SPID, BASE. Stdout/stderr to $OUT/serve-PORT.{out,err}.
serve_start() {
  local port=$1
  ( cd "$W" && exec opencode serve --port "$port" >"$OUT/serve-$port.out" 2>"$OUT/serve-$port.err" </dev/null ) &
  SPID=$!; echo $SPID >>"$PIDS"
  for i in $(seq 1 200); do grep -q 'listening on' "$OUT/serve-$port.out" 2>/dev/null && break; kill -0 $SPID 2>/dev/null || break; sleep 0.1; done
  BASE=http://127.0.0.1:$port
}
api() { local m=$1 p=$2 b=${3-}
  if [ -n "$b" ]; then curl -sS -m ${TMO:-120} -X "$m" -H 'content-type: application/json' -H "x-opencode-directory: $W" -d "$b" "$BASE$p"
  else curl -sS -m ${TMO:-120} -X "$m" -H "x-opencode-directory: $W" "$BASE$p"; fi; }
stop_pid() { kill "$1" 2>/dev/null; for i in $(seq 1 30); do kill -0 "$1" 2>/dev/null || return 0; sleep 0.1; done; kill -9 "$1" 2>/dev/null; }
# tmux driving: a private tmux server (-L oca), 200x50. tui_start LOG [opencode args...] runs the TUI in $W with PROBE_LOG=LOG.
T="tmux -L oca"
tui_start() { local log=$1; shift
  $T kill-server 2>/dev/null
  $T new-session -d -s a -x 200 -y 50 -c "$W" "env PROBE_LOG=$log PROBE_CTL=$PROBE_CTL PROBE_FLAGS=$PROBE_FLAGS XDG_CONFIG_HOME=$XDG_CONFIG_HOME XDG_DATA_HOME=$XDG_DATA_HOME XDG_STATE_HOME=$XDG_STATE_HOME XDG_CACHE_HOME=$XDG_CACHE_HOME OPENCODE_DISABLE_AUTOUPDATE=1 PATH=$PATH opencode $*"
  TUIPID=$($T display -p -t a '#{pane_pid}'); echo "$TUIPID" >>"$PIDS"; echo "$($T display -p '#{pid}')" >>"$PIDS"; }
tkeys() { $T send-keys -t a "$@"; }
tcap() { $T capture-pane -p -t a | redact; }
tui_stop() { $T kill-server 2>/dev/null; }
# ctl JSON: append one command line for the plugin to run
ctl() { echo "$1" >>"$PROBE_CTL"; }
# wait_idle LOG N: until LOG holds N session.idle events (90 s at most)
wait_idle() { for i in $(seq 1 180); do [ "$(jq -c 'select(.name=="session.idle")' "$1" 2>/dev/null | wc -l)" -ge "$2" ] && return 0; sleep 0.5; done; return 1; }
