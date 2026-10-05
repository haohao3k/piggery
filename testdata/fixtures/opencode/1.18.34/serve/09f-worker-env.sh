#!/bin/bash
# Q6: the proposed worker environment, against a "user" config that would otherwise make bash ask (cfg from 09a's scratch dir:
# permission bash:"ask", ~/.claude skills present). Checks, through proxy.py, the tools and the system prompt the model gets and
# that a bash call runs without a permission ask.
. "$(dirname "$0")/lib.sh"; cd "$S"; O=09f-worker-env
[ -d /tmp/oc/xdg-cfg ] || { echo "run 09a-config-merge.sh first (it builds /tmp/oc/xdg-cfg)"; exit 1; }
exec > >(redact > $O.txt) 2>&1
: > $O.proxy.jsonl; python3 proxy.py 47903 $O.proxy.jsonl & PX=$!; echo $PX >>$PIDS; sleep 1
export XDG_CONFIG_HOME=/tmp/oc/xdg-cfg/config
export OPENCODE_SERVER_PASSWORD=pw; AUTH=opencode:pw
export OPENCODE_DISABLE_CLAUDE_CODE=1 OPENCODE_DISABLE_AUTOUPDATE=1 OPENCODE_DISABLE_LSP_DOWNLOAD=1
export OPENCODE_PERMISSION='{"*":"allow","question":"deny","task":"deny","skill":"deny"}'
export OPENCODE_CONFIG_CONTENT='{"provider":{"hp":{"options":{"baseURL":"http://127.0.0.1:47903/v1"}}},"share":"disabled","lsp":false}'
serve_start $O-serve --port 0; sse_start $O.sse.jsonl
echo "config permission: $(api GET /config | python3 -c 'import json,sys;print(json.dumps(json.load(sys.stdin).get("permission")))')"
echo "GET /skill: $(api GET /skill | python3 -c 'import json,sys;print(len(json.load(sys.stdin)))') skills"
SID=$(api POST /session '{"title":"worker"}' | sid)
curl -sS -m 30 -u $AUTH -o /dev/null -w "prompt_async http=%{http_code}\n" -X POST -H 'content-type: application/json' -H "x-opencode-directory: $W" -d '{"model":{"providerID":"hp","modelID":"glm-5.3-flash"},"parts":[{"type":"text","text":"You must call the bash tool now with the command: echo piggery . Do not answer before the tool has run. Then reply with exactly: WORKER-DONE"}]}' $BASE/session/$SID/prompt_async
echo "turn: $(wait_idle $SID 60) ms"; sleep 1; sse_stop
echo "permission events: $(python3 -c '
import json
print([json.loads(l)["data"]["type"] for l in open("'$O.sse.jsonl'") if isinstance(json.loads(l).get("data"),dict) and json.loads(l)["data"]["type"].startswith("permission")])')"
python3 - <<PY
import json
for i,l in enumerate(open("$O.proxy.jsonl")):
    j=json.loads(l); s=[m for m in j["messages"] if m["role"]=="system"]
    print("request",i,"model",j["model"],"tools",sorted(j["tools"]),"system chars",[m["chars"] for m in s],"tail:",[m["tail"][-60:] for m in s])
PY
api GET /session/$SID/message | redact > $O.messages.json; python3 msgs-summary.py $O.messages.json | cut -c1-200
kill $SPID $PX; wait $SPID $PX 2>/dev/null
