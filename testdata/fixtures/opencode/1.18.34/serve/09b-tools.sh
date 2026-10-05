#!/bin/bash
# Q6: which tools the model is really given (proxy.py logs the request), how to take native tools away (config permission
# deny, per-session permission), what a permission "ask" does to a headless session, and that permission "allow" avoids it.
. "$(dirname "$0")/lib.sh"; cd "$S"; O=09b-tools
export OPENCODE_SERVER_PASSWORD=pw; AUTH=opencode:pw
exec > >(redact > $O.txt) 2>&1
: > $O.proxy.jsonl; python3 proxy.py 47902 $O.proxy.jsonl & PX=$!; echo $PX >>$PIDS; sleep 1
DEFBODY='{"title":"t"}'
BASEURL='"provider":{"hp":{"options":{"baseURL":"http://127.0.0.1:47902/v1"}}}'
M='"model":{"providerID":"hp","modelID":"glm-5.3-flash"}'
last_tools() { python3 - <<PY
import json
L=[json.loads(l) for l in open("$O.proxy.jsonl")]
j=L[-1]; print("  requests so far: %d; last request tools (%d): %s" % (len(L), len(j["tools"]), sorted(j["tools"])))
PY
}
pa() { curl -sS -m 30 -u $AUTH -o /dev/null -w "  prompt_async http=%{http_code}\n" -X POST -H 'content-type: application/json' -H "x-opencode-directory: $W" -d '{'"$M"',"parts":[{"type":"text","text":"'"$1"'"}]}' $BASE/session/$SID/prompt_async; }
run() { # run NAME CONFIG_CONTENT [session-create-body]
  export OPENCODE_CONFIG_CONTENT="$2"; serve_start $O-$1 --port 0; echo "## $1: serve pid=$SPID"
  echo "  config permission: $(api GET /config | python3 -c 'import json,sys;print(json.dumps(json.load(sys.stdin).get("permission")))')"
  echo "  /experimental/tool/ids: $(api GET /experimental/tool/ids | tr -d '\n')"
  SID=$(api POST /session "${3:-$DEFBODY}" | sid); echo "  session=$SID"
  pa 'Reply with exactly: OK'; echo "  turn: $(wait_idle $SID 60) ms"; last_tools
  kill $SPID; wait $SPID 2>/dev/null; unset OPENCODE_CONFIG_CONTENT
}
run B1-config-deny '{'"$BASEURL"',"permission":{"*":"allow","question":"deny","task":"deny"}}'
run B2-session-deny '{'"$BASEURL"',"permission":"allow"}' '{"title":"s","permission":[{"permission":"question","pattern":"*","action":"deny"},{"permission":"task","pattern":"*","action":"deny"},{"permission":"webfetch","pattern":"*","action":"deny"}]}'
echo; echo "## B3: default permissions + a bash call outside the project dir (external_directory defaults to ask)"
export OPENCODE_CONFIG_CONTENT='{'"$BASEURL"'}'; serve_start $O-B3 --port 0; sse_start $O-B3.sse.jsonl
SID=$(api POST /session '{"title":"ask"}' | sid)
pa 'Use the bash tool to run exactly: ls /etc/ssl . Then reply with exactly: LS-DONE'
sleep 8; echo "  status after 8 s: $(api GET /session/status)"; api GET /permission | redact > $O-B3.permission-list.json; echo "  GET /permission: $(cut -c1-500 $O-B3.permission-list.json)"
RID=$(python3 -c 'import json;d=json.load(open("'$O-B3.permission-list.json'"));print(d[0]["id"] if d else "")'); echo "  request id: $RID"
echo "  reply once -> $(api POST /permission/$RID/reply '{"reply":"once"}')"
echo "  turn after reply: $(wait_idle $SID 60) ms"; sleep 1; sse_stop
echo "  SSE types around the ask:"; python3 - <<PY
import json
for l in open("$O-B3.sse.jsonl"):
    d=json.loads(l).get("data")
    if isinstance(d,dict) and d.get("type","").startswith(("permission","session.status","session.idle","session.error")): print("   ", d["type"], json.dumps(d.get("properties"))[:260])
PY
kill $SPID; wait $SPID 2>/dev/null
echo; echo "## B3b: the same prompt with permission allow (no ask expected)"
export OPENCODE_CONFIG_CONTENT='{'"$BASEURL"',"permission":"allow"}'; serve_start $O-B3b --port 0
SID=$(api POST /session '{"title":"allow"}' | sid)
pa 'Use the bash tool to run exactly: ls /etc/ssl . Then reply with exactly: LS-DONE'
echo "  turn: $(wait_idle $SID 60) ms; pending permissions: $(api GET /permission)"
api GET /session/$SID/message | redact > $O-B3b.messages.json; python3 msgs-summary.py $O-B3b.messages.json | cut -c1-200
kill $SPID $PX; wait $SPID $PX 2>/dev/null
