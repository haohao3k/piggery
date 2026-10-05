#!/bin/bash
# Q3/Q4: session create (can we choose the id?), prompt_async + SSE (end-of-turn signal), the sync /message answer,
# status polling. One model turn each for A and B.
. "$(dirname "$0")/lib.sh"; cd "$S"; O=03-prompt
export OPENCODE_SERVER_PASSWORD=pw; AUTH=opencode:pw
M='"model":{"providerID":"hp","modelID":"glm-5.3-flash"}'
exec > >(redact > $O.txt) 2>&1
serve_start $O-serve --port 0; echo "serve pid=$SPID port=$PORT start_ms=$START_MS"
sse_start $O.sse.jsonl
echo "## create a session (body may carry id? -> try)"
api POST /session '{"id":"ses_mine_0001","title":"A"}' | redact > $O.create-with-id.json; head -c 400 $O.create-with-id.json; echo
api POST /session '{"title":"A"}' | redact > $O.create.json; SID=$(sid < $O.create.json); echo "session=$SID"; cut -c1-600 $O.create.json; echo
echo "## status before any prompt: $(api GET /session/status)"
echo "## A: prompt_async -> $(date +%s)"; t0=$(now_ms)
curl -sS -m 30 -u $AUTH -o /dev/null -w "prompt_async http=%{http_code} time=%{time_total}s\n" -X POST -H 'content-type: application/json' -H "x-opencode-directory: $W" -d '{'"$M"',"parts":[{"type":"text","text":"Reply with exactly: ALPHA"}]}' $BASE/session/$SID/prompt_async
sleep 0.5; echo "status during: $(api GET /session/status)"
echo "turn took $(wait_idle $SID) ms until absent from /session/status"; echo "status after: $(api GET /session/status)"
api GET /session/$SID/message | redact > $O.A.messages.json; echo "A messages: $(python3 -c 'import json;d=json.load(open("'$O.A.messages.json'"));print([(m["info"]["role"],[p["type"] for p in m["parts"]]) for m in d])')"
echo "## B: sync POST /message (waits for the answer)"
t0=$(now_ms); api POST /session/$SID/message '{'"$M"',"parts":[{"type":"text","text":"Reply with exactly: BRAVO"}]}' | redact > $O.B.sync-response.json
echo "sync /message took $(( $(now_ms)-t0 )) ms, keys: $(python3 -c 'import json;d=json.load(open("'$O.B.sync-response.json'"));print(list(d.keys()), [p["type"] for p in d["parts"]], d["info"].get("finish"), d["info"].get("tokens"))')"
echo "status after sync: $(api GET /session/status)"
sleep 1; sse_stop
echo "## SSE event types in order:"; python3 -c '
import json
for l in open("'$O.sse.jsonl'"):
    d=json.loads(l); x=d.get("data")
    if isinstance(x,dict): print(d["t"]%100000, x.get("type"), (x.get("properties") or {}).get("status") or "")
'
echo "## stop the server"; kill $SPID; wait $SPID 2>/dev/null
