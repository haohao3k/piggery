#!/bin/bash
# Q3: the session id is opencode's (create takes no id); after the server dies, a new serve continues the same session with
# memory, also when the old server was SIGKILLed in the middle of a tool call.
. "$(dirname "$0")/lib.sh"; cd "$S"; O=07-resume
export OPENCODE_SERVER_PASSWORD=pw; AUTH=opencode:pw
M='"model":{"providerID":"hp","modelID":"glm-5.3-flash"}'
exec > >(redact > $O.txt) 2>&1
pa() { curl -sS -m 30 -u $AUTH -o /dev/null -w "prompt_async($1) http=%{http_code}\n" -X POST -H 'content-type: application/json' -H "x-opencode-directory: $W" -d '{'"$M"',"parts":[{"type":"text","text":"'"$2"'"}]}' $BASE/session/$SID/prompt_async; }
echo "## serve 1: a turn that completes, then a turn killed in the middle of a bash tool call"
serve_start $O-serve1 --port 0; S1=$SPID; echo "serve1 pid=$S1 port=$PORT"
SID=$(api POST /session '{"title":"resume"}' | sid); echo "session=$SID"
pa one 'Remember the secret word PLUM42. Reply with exactly: OK1'; echo "turn: $(wait_idle $SID) ms"
pa two 'Use the bash tool to run exactly: sleep 891 . Then reply with exactly: OK2'
sleep 6; echo "status: $(api GET /session/status)"
descendants $S1 > /tmp/oc/desc.txt; SL=$(awk '/sleep 891$/{print $1}' /tmp/oc/desc.txt | head -1); SH=$(awk '/zsh -l -c/{print $1}' /tmp/oc/desc.txt | head -1); echo $SL $SH >>$PIDS
echo "SIGKILL serve1 ($S1) mid-tool; its shell child $SH / sleep $SL are left: alive $(alive $SL) $(alive $SH)"
kill -9 $S1; wait $S1 2>/dev/null; kill $SL $SH 2>/dev/null; sleep 1
echo "## serve 2 (new process, new port), same XDG data dir"
serve_start $O-serve2 --port 0; S2=$SPID; echo "serve2 pid=$S2 port=$PORT"
echo "GET /session/\$SID -> $(api GET /session/$SID | cut -c1-160)"
echo "status of the interrupted session on serve2: $(api GET /session/status)"
api GET /session/$SID/message | redact > $O.before.messages.json; echo "## history as serve2 sees it (before any prompt)"; python3 msgs-summary.py $O.before.messages.json | cut -c1-200
python3 - <<PY
import json
for m in json.load(open("$O.before.messages.json")):
    for p in m["parts"]:
        if p["type"]=="tool": print("tool part state of the killed call:", json.dumps(p["state"])[:300])
PY
pa three 'What was the secret word I told you earlier? Answer with the word only.'; echo "turn: $(wait_idle $SID) ms"
api GET /session/$SID/message | redact > $O.after.messages.json; echo "## history after the new prompt"; python3 msgs-summary.py $O.after.messages.json | cut -c1-200
echo "## stop serve2"; kill $S2; wait $S2 2>/dev/null; echo "alive: $(alive $S2)"
