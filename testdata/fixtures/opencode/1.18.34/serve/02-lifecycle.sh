#!/bin/bash
# Q1b: password auth, then what each way of ending `opencode serve` does to it and to its children
# (a fake MCP server and a /session/:id/shell `sleep`, both started by that server).
. "$(dirname "$0")/lib.sh"; cd "$S"; O=02-lifecycle
MCPPID=/tmp/oc/fake-mcp.pid
CFG='{"mcp":{"fake":{"type":"local","command":["python3","'$S'/fake-mcp.py"],"environment":{"FAKE_MCP_PID_FILE":"'$MCPPID'"}}}}'
exec > >(redact > $O.txt) 2>&1

echo "## auth: OPENCODE_SERVER_PASSWORD=pw (username default), then OPENCODE_SERVER_USERNAME=bob"
export OPENCODE_SERVER_PASSWORD=pw
serve_start $O-auth --port 0; P=$SPID; echo "serve pid=$P port=$PORT start_ms=$START_MS"; cat $O-auth.out
for h in "" "-u wrong:pw" "-u opencode:wrong" "-u opencode:pw"; do
  echo "GET /global/health $h -> $(curl -s -m 5 -o /dev/null -w '%{http_code}' $h $BASE/global/health)"; done
echo "GET /event without auth -> $(curl -s -m 5 -o /dev/null -w '%{http_code}' $BASE/event)"
kill $P; wait $P 2>/dev/null
export OPENCODE_SERVER_USERNAME=bob
serve_start $O-auth2 --port 0; P=$SPID
for h in "-u opencode:pw" "-u bob:pw"; do echo "username=bob: GET /global/health $h -> $(curl -s -m 5 -o /dev/null -w '%{http_code}' $h $BASE/global/health)"; done
kill $P; wait $P 2>/dev/null
unset OPENCODE_SERVER_PASSWORD OPENCODE_SERVER_USERNAME

N=0
# setup: a server with the fake MCP and a running shell child (unique sleep length per scenario so the pid is certain).
setup() {
  N=$((N+1)); SLP=77$N
  rm -f $MCPPID $MCPPID.log
  export OPENCODE_CONFIG_CONTENT=$CFG
  serve_start $O-$1 --port 0; SP=$SPID
  SID=$(api POST /session '{"title":"life"}' | python3 -c 'import json,sys;print(json.load(sys.stdin)["id"])')
  api POST /session/$SID/shell '{"agent":"build","command":"sleep '$SLP'"}' >/dev/null 2>&1 &
  sleep 3; api GET /mcp > $O-$1.mcp-status.json
  MP=$(cat $MCPPID 2>/dev/null)
  descendants $SP > /tmp/oc/desc.txt
  SL=$(awk -v s="sleep $SLP" '$0 ~ s"$" {print $1}' /tmp/oc/desc.txt | head -1)
  SH=$(awk '/zsh -l -c/ {print $1}' /tmp/oc/desc.txt | head -1)
  echo $SP $MP $SL $SH >>$PIDS
  echo "serve=$SP port=$PORT start_ms=$START_MS session=$SID mcp_pid=$MP shell_pid=$SH sleep_pid=$SL"
  echo "descendants of serve:"; cut -c1-110 /tmp/oc/desc.txt
  echo "mcp status: $(cat $O-$1.mcp-status.json)"
}
report() { sleep ${1:-4}
  echo "after: serve=$(alive $SP) mcp=$([ -n "$MP" ] && alive $MP) shell(zsh)=$([ -n "$SH" ] && alive $SH) sleep=$([ -n "$SL" ] && alive $SL)"
  [ -f $MCPPID.log ] && echo "mcp log: $(tr '\n' ';' < $MCPPID.log)"
  for p in $SL $SH $MP $SP; do [ -n "$p" ] && kill -0 $p 2>/dev/null && { echo "  leftover $p killed (-9)"; kill -9 $p; }; done; unset OPENCODE_CONFIG_CONTENT; sleep 1; }
for sig in TERM INT HUP; do
  echo; echo "## SIG$sig to serve"; setup $sig
  t0=$(now_ms); kill -$sig $SP; for i in $(seq 1 100); do kill -0 $SP 2>/dev/null || break; sleep 0.1; done; echo "serve gone after $(( $(now_ms)-t0 )) ms (100 polls of 100 ms max)"
  report
done
echo; echo "## SIGKILL to serve"; setup KILL; kill -9 $SP; report

echo; echo "## pids left from this script (from the pid file):"; for p in $(cat $PIDS); do kill -0 $p 2>/dev/null && ps -o pid=,command= -p $p | cut -c1-100; done; echo end
