#!/bin/bash
# A model switch the model never sees: prompt_async {noReply:true, model, variant, parts:[one synthetic+ignored text]} updates the
# session's model (no turn, no model call), and the next prompt (no model in its body) runs on that model while the ignored text is
# nowhere in the request (proxy.py PROXY_NEEDLE). The request is logged before it is forwarded, so what is captured is what opencode
# sent; the committed proxy.py has a placeholder upstream, so with it the model call itself fails (APIError, retried) and costs nothing.
. "$(dirname "$0")/lib.sh"; cd "$S"; O=11-ignored-switch
export OPENCODE_SERVER_PASSWORD=pw; AUTH=opencode:pw
exec > >(redact > $O.txt) 2>&1
: > $O.proxy.jsonl; PROXY_NEEDLE='piggery: model switch' python3 proxy.py 47904 $O.proxy.jsonl & PX=$!; echo $PX >>$PIDS; sleep 1
export OPENCODE_CONFIG_CONTENT='{"provider":{"hp":{"options":{"baseURL":"http://127.0.0.1:47904/v1"},"models":{"alt":{"id":"glm-5.3-flash","name":"alt","reasoning":true,"limit":{"context":500000,"output":128000},"variants":{"low":{"reasoningEffort":"low"},"high":{"reasoningEffort":"high"}}}}}}}'
serve_start $O-serve --port 0; sse_start $O.sse.jsonl
SID=$(api POST /session '{"title":"switch","model":{"id":"glm-5.3-flash","providerID":"hp"}}' | sid); echo "session=$SID"
echo "switch -> $(curl -sS -m 30 -u $AUTH -o /dev/null -w '%{http_code}' -X POST -H 'content-type: application/json' -H "x-opencode-directory: $W" -d '{"noReply":true,"model":{"providerID":"hp","modelID":"alt"},"variant":"high","parts":[{"type":"text","text":"piggery: model switch","synthetic":true,"ignored":true}]}' $BASE/session/$SID/prompt_async)"
sleep 1; echo "session model: $(api GET /session/$SID | python3 -c 'import json,sys;print(json.load(sys.stdin).get("model"))')"
echo "proxy requests so far (a noReply makes none): $(wc -l < $O.proxy.jsonl | tr -d ' ')"
echo "next prompt, no model in the body, variant passed as the plugin would:"
curl -sS -m 30 -u $AUTH -o /dev/null -w "prompt_async http=%{http_code}\n" -X POST -H 'content-type: application/json' -H "x-opencode-directory: $W" -d '{"variant":"high","parts":[{"type":"text","text":"Reply with exactly: SWITCHED"}]}' $BASE/session/$SID/prompt_async
echo "turn: $(wait_idle $SID 60) ms"; sleep 1; sse_stop
python3 - <<PY
import json
for i,l in enumerate(open("$O.proxy.jsonl")):
    j=json.loads(l); print("request",i,"model",j["model"],"reasoning_effort",j.get("reasoning_effort"),"needle_found",j.get("needle_found"),"messages",[(m["role"],m["chars"]) for m in j["messages"]])
PY
api GET /session/$SID/message | redact > $O.messages.json; python3 msgs-summary.py $O.messages.json | cut -c1-200
kill $SPID $PX; wait $SPID $PX 2>/dev/null
