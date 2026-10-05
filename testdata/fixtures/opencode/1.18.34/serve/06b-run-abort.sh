#!/bin/bash
# Q2: aborting `opencode run` (SIGINT / SIGTERM) in the middle of a long tool call, and what is left. (Steps 5-6 of 06-run.sh,
# rerun here because 06-run.txt's step 5 wrote its output files into the work dir by mistake.)
. "$(dirname "$0")/lib.sh"; cd "$S"; O=06b-run-abort
exec > >(redact > $O.txt) 2>&1
MODEL=hp/glm-5.3-flash
sidof() { python3 -c 'import json,sys
for l in open(sys.argv[1]):
    d=json.loads(l)
    if d.get("sessionID"): print(d["sessionID"]); break' $O.$1.jsonl; }
export OPENCODE_SERVER_PASSWORD=pw; AUTH=opencode:pw
serve_start $O-serve --port 0; echo "serve (only to read the sessions afterwards) pid=$SPID port=$PORT"
echo "## 1: abort a run in a long tool call: SIGINT after 7 s"
( cd $W; exec python3 -c 'import os,signal,sys;signal.signal(signal.SIGINT,signal.SIG_DFL);os.execvp("opencode",["opencode","run","--format","json","-m","'$MODEL'","--title","run-abort","Use the bash tool to run exactly: sleep 889 . Then reply with exactly: DONE"])' >$S/$O.r4.jsonl 2>$S/$O.r4.err </dev/null ) & RP=$!; echo $RP >>$PIDS
sleep 7; descendants $RP | rg 'sleep 889$' | cut -c1-80 | sed 's/^/before SIGINT: /'; SL=$(descendants $RP | awk '/sleep 889$/{print $1}' | head -1); echo $SL >>$PIDS
t0=$(now_ms); kill -INT $RP; for i in $(seq 1 100); do kill -0 $RP 2>/dev/null || break; sleep 0.1; done; wait $RP 2>/dev/null; echo "run exit=$? after $(( $(now_ms)-t0 )) ms; run alive=$(alive $RP); sleep child alive=$([ -n "$SL" ] && alive $SL)"
[ -n "$SL" ] && kill $SL 2>/dev/null
echo "last events: $(tail -3 $O.r4.jsonl | cut -c1-200)"; echo "stderr: $(head -c 300 $O.r4.err)"
S4=$(sidof r4); echo "run-abort session=$S4; its status/last assistant error via the serve:"
api GET /session/$S4/message | redact > $O.r4.messages.json; python3 msgs-summary.py $O.r4.messages.json | cut -c1-170
echo "## 2: abort a run with SIGTERM after 7 s"
( cd $W; exec opencode run --format json -m $MODEL --title run-term "Use the bash tool to run exactly: sleep 890 . Then reply with exactly: DONE" >$S/$O.r5.jsonl 2>$S/$O.r5.err </dev/null ) & RP=$!; echo $RP >>$PIDS
sleep 7; SL=$(descendants $RP | awk '/sleep 890$/{print $1}' | head -1); echo $SL >>$PIDS; echo "before SIGTERM: run=$RP sleep=$SL"
kill -TERM $RP; sleep 2; wait $RP 2>/dev/null; echo "run exit=$? alive=$(alive $RP); sleep child alive=$([ -n "$SL" ] && alive $SL)"; [ -n "$SL" ] && kill $SL 2>/dev/null
kill $SPID; wait $SPID 2>/dev/null
