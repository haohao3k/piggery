#!/bin/bash
# Q4: a second prompt_async while the session is busy (inside a long bash tool call): is it steered in, and answered once?
. "$(dirname "$0")/lib.sh"; cd "$S"; O=04-steer
export OPENCODE_SERVER_PASSWORD=pw; AUTH=opencode:pw
M='"model":{"providerID":"hp","modelID":"glm-5.3-flash"}'
exec > >(redact > $O.txt) 2>&1
serve_start $O-serve --port 0; echo "serve pid=$SPID port=$PORT start_ms=$START_MS"
sse_start $O.sse.jsonl
SID=$(api POST /session '{"title":"steer"}' | sid); echo "session=$SID"
pa() { curl -sS -m 30 -u $AUTH -o /dev/null -w "prompt_async($1) http=%{http_code} at=$(now_ms)\n" -X POST -H 'content-type: application/json' -H "x-opencode-directory: $W" -d '{'"$M"',"parts":[{"type":"text","text":"'"$2"'"}]}' $BASE/session/$SID/prompt_async; }
T0=$(now_ms)
pa first 'Use the bash tool to run exactly: sleep 12 && echo FIRST-DONE . When it returns, reply with exactly: ONE'
sleep 6; echo "status at +6s: $(api GET /session/status)"
pa second 'Additional instruction: in your final reply also say the word TWO-STEERED'
echo "turn took $(wait_idle $SID 150) ms after the second prompt (total $(( $(now_ms)-T0 )) ms)"
sleep 1; sse_stop
api GET /session/$SID/message | redact > $O.messages.json; echo "## messages"; python3 msgs-summary.py $O.messages.json
echo "## SSE (session-level event types, time since first prompt):"
python3 - <<PY
import json
t0=$T0
for l in open("$O.sse.jsonl"):
    d=json.loads(l); x=d.get("data")
    if not isinstance(x,dict): continue
    ty=x.get("type"); p=x.get("properties") or {}
    if ty in ("message.part.delta","server.heartbeat"): continue
    extra=p.get("status") or (p.get("info") or {}).get("role") or (p.get("part") or {}).get("type") or ""
    print("%6d %s %s"%(d["t"]-t0,ty,extra))
PY
kill $SPID; wait $SPID 2>/dev/null
