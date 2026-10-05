#!/bin/bash
# Q2/Q3: `opencode run --format json --session <id>` per batch: latency, memory across processes, sharing the session store
# with a serve, `run --attach`, abort of a run (SIGINT/SIGTERM), and a second `run` on a session that a first `run` is still working on.
. "$(dirname "$0")/lib.sh"; cd "$S"; O=06-run
exec > >(redact > $O.txt) 2>&1
MODEL=hp/glm-5.3-flash
timed() { # timed NAME cmd... : stdout -> $O.NAME.jsonl, stderr -> $O.NAME.err, prints wall ms and exit code
  local n=$1; shift; local t0; t0=$(now_ms)
  ( cd $W && "$@" >$S/$O.$n.jsonl 2>$S/$O.$n.err </dev/null ); local rc=$?
  echo "[$n] wall=$(( $(now_ms)-t0 )) ms exit=$rc events=$(wc -l < $O.$n.jsonl | tr -d ' ') stderr_bytes=$(wc -c < $O.$n.err | tr -d ' ')"
}
sidof() { python3 -c 'import json,sys
for l in open(sys.argv[1]):
    d=json.loads(l)
    if d.get("sessionID"): print(d["sessionID"]); break' $O.$1.jsonl; }
echo "## 1: first run (new session)"
timed r1 opencode run --format json -m $MODEL --title run-batch "Remember the secret word PLUM42. Reply with exactly: R1"
SID=$(sidof r1); echo "session=$SID"
echo "## 2: --session <id>, a new process: does it remember? latency of a batch"
timed r2 opencode run --format json -m $MODEL --session $SID "What was the secret word? Answer with the word only."
python3 -c 'import json
for l in open("'$O.r2.jsonl'"):
    d=json.loads(l)
    if d["type"]=="text": print("answer:", d["part"]["text"])
    if d["type"]=="step_finish": print("tokens:", d["part"]["tokens"])'
echo "event types in r1: $(python3 -c 'import json;print([json.loads(l)["type"] for l in open("'$O.r1.jsonl'")])')"
echo "## 3: the same session store in a serve (no copy): GET /session/\$SID and a prompt there"
export OPENCODE_SERVER_PASSWORD=pw; AUTH=opencode:pw
serve_start $O-serve --port 0; echo "serve pid=$SPID port=$PORT"
api GET /session/$SID | cut -c1-200; echo
api GET /session/$SID/message | redact > $O.serve-view.messages.json; python3 msgs-summary.py $O.serve-view.messages.json | cut -c1-160
echo "## 4: run --attach <serve> --session (no process start cost?)"
timed r3 opencode run --format json -m $MODEL --attach $BASE -p pw --dir $W --session $SID "Reply with exactly: R3"
echo "## 5: abort a run in a long tool call: SIGINT after 7 s"
( cd $W; exec python3 -c 'import os,signal,sys;signal.signal(signal.SIGINT,signal.SIG_DFL);os.execvp("opencode",["opencode","run","--format","json","-m","'$MODEL'","--title","run-abort","Use the bash tool to run exactly: sleep 889 . Then reply with exactly: DONE"])' >$O.r4.jsonl 2>$O.r4.err </dev/null ) & RP=$!; echo $RP >>$PIDS
sleep 7; descendants $RP | rg 'sleep 889$' | cut -c1-80 | sed 's/^/before SIGINT: /'; SL=$(descendants $RP | awk '/sleep 889$/{print $1}' | head -1); echo $SL >>$PIDS
t0=$(now_ms); kill -INT $RP; for i in $(seq 1 100); do kill -0 $RP 2>/dev/null || break; sleep 0.1; done; wait $RP 2>/dev/null; echo "run exit=$? after $(( $(now_ms)-t0 )) ms; run alive=$(alive $RP); sleep child alive=$([ -n "$SL" ] && alive $SL)"
[ -n "$SL" ] && kill $SL 2>/dev/null
echo "last events: $(tail -3 $O.r4.jsonl | cut -c1-200)"; echo "stderr: $(head -c 300 $O.r4.err)"
S4=$(sidof r4); echo "run-abort session=$S4; its status/last assistant error via the serve:"
api GET /session/$S4/message | redact > $O.r4.messages.json; python3 msgs-summary.py $O.r4.messages.json | cut -c1-170
echo "## 6: abort a run with SIGTERM after 7 s"
( cd $W; exec opencode run --format json -m $MODEL --title run-term "Use the bash tool to run exactly: sleep 890 . Then reply with exactly: DONE" >$O.r5.jsonl 2>$O.r5.err </dev/null ) & RP=$!; echo $RP >>$PIDS
sleep 7; SL=$(descendants $RP | awk '/sleep 890$/{print $1}' | head -1); echo $SL >>$PIDS; echo "before SIGTERM: run=$RP sleep=$SL"
kill -TERM $RP; sleep 2; wait $RP 2>/dev/null; echo "run exit=$? alive=$(alive $RP); sleep child alive=$([ -n "$SL" ] && alive $SL)"; [ -n "$SL" ] && kill $SL 2>/dev/null
echo "## 7: a second run on the same session while a first run is inside a long tool call (steer by run?)"
S7=$(api POST /session '{"title":"two-runs"}' | sid); echo "session=$S7"
( cd $W; exec opencode run --format json -m $MODEL --session $S7 "Use the bash tool to run exactly: sleep 10 && echo ONE-DONE . Then reply with exactly: FIRST" >$S/$O.r6a.jsonl 2>$S/$O.r6a.err </dev/null ) & RA=$!; echo $RA >>$PIDS
sleep 5; echo "status via serve at +5 s: $(api GET /session/status)"
timed r6b opencode run --format json -m $MODEL --session $S7 "Reply with exactly: SECOND"
wait $RA 2>/dev/null; echo "first run exit=$?"
api GET /session/$S7/message | redact > $O.r6.messages.json; python3 msgs-summary.py $O.r6.messages.json | cut -c1-170
echo "## stop the serve"; kill $SPID; wait $SPID 2>/dev/null
