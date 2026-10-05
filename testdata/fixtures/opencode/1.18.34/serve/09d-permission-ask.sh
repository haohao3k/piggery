#!/bin/bash
# Q6: what a permission "ask" does to a headless session (config bash:"ask"): the event, the pending list, the reply route,
# and that without a reply the turn waits (so workers need permission allow, or something must answer).
. "$(dirname "$0")/lib.sh"; cd "$S"; O=09d-permission-ask
export OPENCODE_SERVER_PASSWORD=pw; AUTH=opencode:pw
exec > >(redact > $O.txt) 2>&1
export OPENCODE_CONFIG_CONTENT='{"permission":{"bash":"ask"}}'
serve_start $O-serve --port 0; sse_start $O.sse.jsonl
SID=$(api POST /session '{"title":"ask"}' | sid); echo "session=$SID"
curl -sS -m 30 -u $AUTH -o /dev/null -w "prompt_async http=%{http_code}\n" -X POST -H 'content-type: application/json' -H "x-opencode-directory: $W" -d '{"model":{"providerID":"hp","modelID":"glm-5.3-flash"},"parts":[{"type":"text","text":"You must call the bash tool now with the command: touch /tmp/oc/work/asked.txt . Do not answer before the tool has run. Then reply with exactly: ASK-DONE"}]}' $BASE/session/$SID/prompt_async
sleep 9; echo "status after 9 s (still busy = waiting for a reply): $(api GET /session/status)"
api GET /permission | redact > $O.permission-list.json; echo "GET /permission: $(cut -c1-600 $O.permission-list.json)"
RID=$(python3 -c 'import json;d=json.load(open("'$O.permission-list.json'"));print(d[0]["id"] if d else "")'); echo "request id: $RID"
if [ -n "$RID" ]; then echo "POST /permission/\$RID/reply {reply:once} -> $(api POST /permission/$RID/reply '{"reply":"once"}')"; fi
echo "turn after reply: $(wait_idle $SID 60) ms"; sleep 1; sse_stop
echo "## SSE around the ask"; python3 - <<PY
import json
for l in open("$O.sse.jsonl"):
    d=json.loads(l).get("data")
    if isinstance(d,dict) and (d["type"].startswith("permission") or d["type"] in ("session.status","session.idle","session.error")): print(" ", d["type"], json.dumps(d.get("properties"))[:400])
PY
api GET /session/$SID/message | redact > $O.messages.json; python3 msgs-summary.py $O.messages.json | cut -c1-200
kill $SPID; wait $SPID 2>/dev/null
