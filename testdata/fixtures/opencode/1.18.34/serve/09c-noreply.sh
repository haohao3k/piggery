#!/bin/bash
# prompt_async with noReply:true (the message is stored, no model call, no turn) and a client-chosen messageID. No model turns.
. "$(dirname "$0")/lib.sh"; cd "$S"; O=09c-noreply
export OPENCODE_SERVER_PASSWORD=pw; AUTH=opencode:pw
exec > >(redact > $O.txt) 2>&1
serve_start $O-serve --port 0; sse_start $O.sse.jsonl
SID=$(api POST /session '{"title":"noreply"}' | sid); echo "session=$SID"
MID=$(python3 -c 'import time;print("msg_%012x%s"%((int(time.time()*1000)*0x1000+1)&0xffffffffffff,"PiGgErY00000AB"))'); echo "chosen messageID=$MID"
curl -sS -m 30 -u $AUTH -o /dev/null -w "prompt_async noReply http=%{http_code}\n" -X POST -H 'content-type: application/json' -H "x-opencode-directory: $W" -d '{"messageID":"'$MID'","noReply":true,"model":{"providerID":"hp","modelID":"glm-5.3-flash"},"parts":[{"type":"text","text":"mail: hello (stored, no turn)"}]}' $BASE/session/$SID/prompt_async
sleep 2; echo "status: $(api GET /session/status)"
api GET /session/$SID/message | redact > $O.messages.json; python3 msgs-summary.py $O.messages.json | cut -c1-200
python3 -c 'import json;d=json.load(open("'$O.messages.json'"));print("stored id:", d[0]["info"]["id"], "== chosen:", d[0]["info"]["id"]=="'$MID'")'
echo "SSE types: $(python3 -c '
import json
print([json.loads(l)["data"]["type"] for l in open("'$O.sse.jsonl'") if isinstance(json.loads(l).get("data"),dict) and json.loads(l)["data"]["type"] not in ("plugin.added",)])')"
sse_stop; kill $SPID; wait $SPID 2>/dev/null
