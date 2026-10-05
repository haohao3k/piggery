#!/bin/bash
# Q4: POST /session/:id/abort in the middle of a long bash tool call: events, result, the tool's child process, the
# session afterwards (a follow-up prompt works), and what abort does with a prompt queued behind it.
. "$(dirname "$0")/lib.sh"; cd "$S"; O=05-abort
export OPENCODE_SERVER_PASSWORD=pw; AUTH=opencode:pw
M='"model":{"providerID":"hp","modelID":"glm-5.3-flash"}'
exec > >(redact > $O.txt) 2>&1
serve_start $O-serve --port 0; echo "serve pid=$SPID port=$PORT start_ms=$START_MS"
sse_start $O.sse.jsonl
SID=$(api POST /session '{"title":"abort"}' | sid); echo "session=$SID"
pa() { curl -sS -m 30 -u $AUTH -o /dev/null -w "prompt_async($1) http=%{http_code}\n" -X POST -H 'content-type: application/json' -H "x-opencode-directory: $W" -d '{'"$M"',"parts":[{"type":"text","text":"'"$2"'"}]}' $BASE/session/$SID/prompt_async; }
T0=$(now_ms)
pa long 'Use the bash tool to run exactly: sleep 888 . Then reply with exactly: LONG-DONE'
sleep 7
descendants $SPID | rg 'sleep 888$' | cut -c1-80 | sed 's/^/before abort: /'
SL=$(descendants $SPID | awk '/sleep 888$/{print $1}'|head -1); echo $SL >>$PIDS
echo "status: $(api GET /session/status)"
pa queued 'After the abort, reply with exactly: SHOULD-NOT-RUN'
t1=$(now_ms); echo "abort -> $(api POST /session/$SID/abort) in $(( $(now_ms)-t1 )) ms (at +$(( t1-T0 )) ms)"
sleep 2; echo "status after abort: $(api GET /session/status)"
echo "sleep 888 after abort: $([ -n "$SL" ] && alive $SL)"; [ -n "$SL" ] && kill $SL 2>/dev/null
echo "abort again on an idle session -> $(api POST /session/$SID/abort)"
echo "## follow-up prompt after abort:"; pa after 'Reply with exactly: AFTER-ABORT'
echo "turn took $(wait_idle $SID 90) ms"
sleep 1; sse_stop
api GET /session/$SID/message | redact > $O.messages.json; echo "## messages"; python3 msgs-summary.py $O.messages.json
echo "## SSE (time since the first prompt):"
python3 - <<PY
import json
t0=$T0
for l in open("$O.sse.jsonl"):
    d=json.loads(l); x=d.get("data")
    if not isinstance(x,dict): continue
    ty=x.get("type"); p=x.get("properties") or {}
    if ty in ("message.part.delta","server.heartbeat"): continue
    extra=p.get("status") or (p.get("info") or {}).get("role") or (p.get("part") or {}).get("type") or (p.get("error") or {}).get("name") or ""
    print("%6d %s %s"%(d["t"]-t0,ty,extra))
PY
kill $SPID; wait $SPID 2>/dev/null
