#!/bin/bash
# Q5: a prompt naming a model that does not exist (no network call): what the client sees (SSE, message list, status).
. "$(dirname "$0")/lib.sh"; cd "$S"; O=08b-bad-model
export OPENCODE_SERVER_PASSWORD=pw; AUTH=opencode:pw
exec > >(redact > $O.txt) 2>&1
serve_start $O-serve --port 0; sse_start $O.sse.jsonl
SID=$(api POST /session '{"title":"bad"}' | sid); echo "session=$SID"
for body in '{"model":{"providerID":"hp","modelID":"nonexistent"},"parts":[{"type":"text","text":"hi"}]}' '{"model":{"providerID":"nope","modelID":"x"},"parts":[{"type":"text","text":"hi"}]}'; do
  echo "prompt_async $body -> $(curl -sS -m 30 -u $AUTH -w ' http=%{http_code}' -X POST -H 'content-type: application/json' -H "x-opencode-directory: $W" -d "$body" $BASE/session/$SID/prompt_async)"; sleep 3
  echo "  status: $(api GET /session/status)"
done
echo "sync /message with the bad model -> $(TMO=30 api POST /session/$SID/message '{"model":{"providerID":"hp","modelID":"nonexistent"},"parts":[{"type":"text","text":"hi"}]}' | cut -c1-400)"
sleep 1; sse_stop
echo "## SSE (no plugin.added/delta)"; python3 - <<PY
import json
for l in open("$O.sse.jsonl"):
    d=json.loads(l).get("data")
    if isinstance(d,dict) and d["type"] not in ("plugin.added","session.updated","session.diff","server.heartbeat","catalog.updated","reference.updated","integration.updated"): print(" ", d["type"], json.dumps(d.get("properties"))[:300])
PY
api GET /session/$SID/message | redact > $O.messages.json; python3 msgs-summary.py $O.messages.json | cut -c1-200
kill $SPID; wait $SPID 2>/dev/null
