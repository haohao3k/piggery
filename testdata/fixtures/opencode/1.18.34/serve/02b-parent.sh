#!/bin/bash
# Q1c: parent death and stdin EOF. Same helpers as 02-lifecycle.sh.
. "$(dirname "$0")/lib.sh"; cd "$S"; O=02b-parent
MCPPID=/tmp/oc/fake-mcp.pid
CFG='{"mcp":{"fake":{"type":"local","command":["python3","'$S'/fake-mcp.py"],"environment":{"FAKE_MCP_PID_FILE":"'$MCPPID'"}}}}'
exec > >(redact > $O.txt) 2>&1
report() { sleep ${1:-4}
  echo "after: serve=$(alive $SP) mcp=$([ -n "$MP" ] && alive $MP) shell(zsh)=$([ -n "$SH" ] && alive $SH) sleep=$([ -n "$SL" ] && alive $SL)"
  [ -f $MCPPID.log ] && echo "mcp log: $(tr '\n' ';' < $MCPPID.log)"
  for p in $SL $SH $MP $SP; do [ -n "$p" ] && kill -0 $p 2>/dev/null && { echo "  leftover $p killed (-9)"; kill -9 $p; }; done; unset OPENCODE_CONFIG_CONTENT; sleep 1; }
echo
echo; echo "## parent death: a python parent (stdin/stdout/stderr pipes to serve, like the runner) is SIGKILLed"
export OPENCODE_CONFIG_CONTENT=$CFG; rm -f $MCPPID $MCPPID.log
python3 parent.py /tmp/oc/parent.info $W & PP=$!; echo $PP >>$PIDS; sleep 3
read SP PORT < /tmp/oc/parent.info; echo $SP >>$PIDS; BASE=http://127.0.0.1:$PORT
echo "parent=$PP serve=$SP port=$PORT health=$(curl -s -m 5 $BASE/global/health)"
SID=$(api POST /session '{"title":"pd"}' | python3 -c 'import json,sys;print(json.load(sys.stdin)["id"])')
api POST /session/$SID/shell '{"agent":"build","command":"sleep 7799"}' >/dev/null 2>&1 &
sleep 3; api GET /mcp >/dev/null; sleep 1; MP=$(cat $MCPPID 2>/dev/null); descendants $SP > /tmp/oc/desc.txt
SL=$(awk '/sleep 7799$/ {print $1}' /tmp/oc/desc.txt | head -1); SH=$(awk '/zsh -l -c/ {print $1}' /tmp/oc/desc.txt | head -1); echo $MP $SL $SH >>$PIDS
echo "mcp=$MP shell=$SH sleep=$SL"
kill -9 $PP; sleep 3
echo "serve after the parent was killed: $(alive $SP) ppid=$(ps -o ppid= -p $SP | tr -d ' ')  health=$(curl -s -m 5 $BASE/global/health)"
echo "still works (session list count): $(api GET /session | python3 -c 'import json,sys;print(len(json.load(sys.stdin)))')"
echo "create session -> $(api POST /session '{"title":"pd2"}' | cut -c1-80)"
report 1
unset OPENCODE_CONFIG_CONTENT

echo; echo "## stdin EOF: serve's stdin is a pipe that closes after 2 s"
( cd $W; sleep 2 | python3 -c 'import os,signal,sys;os.execvp("opencode",["opencode","serve","--port","0"])' >$S/$O-eof.out 2>$S/$O-eof.err ) & 
sleep 1; for i in $(seq 1 20); do grep -q listening $S/$O-eof.out 2>/dev/null && break; sleep .2; done
PORT=$(sed -n 's/.*:\([0-9]*\)$/\1/p' $S/$O-eof.out | tail -1); SP=$(lsof -nP -iTCP:$PORT -sTCP:LISTEN -t | head -1); echo $SP >>$PIDS
sleep 5; echo "serve $SP (port $PORT) 5 s after stdin EOF: $(alive $SP), health=$(curl -s -m 5 http://127.0.0.1:$PORT/global/health)"
kill $SP; sleep 1


echo; echo "## pids left from this script (from the pid file):"; for p in $(cat $PIDS); do kill -0 $p 2>/dev/null && ps -o pid=,command= -p $p | cut -c1-100; done; echo end
