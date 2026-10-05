#!/bin/bash
# Q5: the models list (for list_models), model per prompt (body.model), variant, per-prompt system, and what opencode really
# sends (through proxy.py). hp/alt is an alias of glm-5.3-flash defined by OPENCODE_CONFIG_CONTENT with reasoning:true and
# variants low/high, so a variant can be exercised with the one allowed model.
. "$(dirname "$0")/lib.sh"; cd "$S"; O=08-models
export OPENCODE_SERVER_PASSWORD=pw; AUTH=opencode:pw
exec > >(redact > $O.txt) 2>&1
: > $O.proxy.jsonl; python3 proxy.py 47901 $O.proxy.jsonl & PX=$!; echo $PX >>$PIDS; sleep 1
export OPENCODE_CONFIG_CONTENT='{"provider":{"hp":{"options":{"baseURL":"http://127.0.0.1:47901/v1"},"models":{"alt":{"id":"glm-5.3-flash","name":"alt (alias, reasoning)","reasoning":true,"limit":{"context":500000,"output":128000},"variants":{"low":{"reasoningEffort":"low"},"high":{"reasoningEffort":"high"}}}}}}}'
echo "## opencode models (CLI)"; ( cd $W; opencode models hp 2>&1 | head -20; echo "-- verbose:"; opencode models hp --verbose 2>&1 | head -60 | cut -c1-160 )
serve_start $O-serve --port 0; echo "serve pid=$SPID port=$PORT"
echo "## GET /config/providers"; api GET /config/providers | redact > $O.config-providers.json; python3 - <<PY
import json
d=json.load(open("$O.config-providers.json")); print("top keys:", list(d.keys()), "default:", d.get("default"))
for p in d["providers"]:
    print(p["id"], "models:", {k:{"name":v.get("name"),"variants":list((v.get("variants") or {}).keys()),"reasoning":(v.get("capabilities") or v).get("reasoning"),"limit":v.get("limit")} for k,v in p["models"].items()}, "keys of provider:", list(p.keys()))
PY
echo "## GET /provider (connected, ids only)"; api GET /provider | redact > $O.provider.json; python3 -c '
import json; d=json.load(open("'$O.provider.json'")); print("keys:", list(d.keys()), "connected:", d.get("connected"), "all providers:", len(d.get("all",[])), "default:", d.get("default"))
keep=set(d["connected"]); json.dump({"connected":d["connected"],"default":{k:v for k,v in d["default"].items() if k in keep},"all":[x for x in d["all"] if x["id"] in keep]},open("'$O.provider.json'","w"),indent=1)'
echo "## GET /config (apiKey must not show up resolved)"; api GET /config | redact > $O.config.json; python3 -c '
import json; d=json.load(open("'$O.config.json'")); print("keys:", sorted(d.keys())); print("hp options keys:", list(d["provider"]["hp"]["options"].keys()))'
SID=$(api POST /session '{"title":"models"}' | sid); echo "session=$SID"
pa() { echo "-- prompt_async($1) body model/variant: $3 -> $(curl -sS -m 30 -u $AUTH -o /dev/null -w '%{http_code}' -X POST -H 'content-type: application/json' -H "x-opencode-directory: $W" -d '{'"$3"',"parts":[{"type":"text","text":"Reply with exactly: '"$2"'"}]}' $BASE/session/$SID/prompt_async) turn=$(wait_idle $SID 60)ms"; }
pa P1 ONE '"model":{"providerID":"hp","modelID":"glm-5.3-flash"}'
pa P2 TWO '"model":{"providerID":"hp","modelID":"alt"},"variant":"high"'
pa P3 THREE '"model":{"providerID":"hp","modelID":"alt"},"variant":"low"'
pa P4 FOUR '"model":{"providerID":"hp","modelID":"glm-5.3-flash"},"variant":"high"'
pa P5 FIVE '"model":{"providerID":"hp","modelID":"nonexistent"}'
pa P6 SIX '"model":{"providerID":"hp","modelID":"alt"},"system":"You are the piggery test role. Always append the word ROLECARD."'
sleep 1
echo "## what reached the model API (one line per request: model, reasoning keys, #tools, system heads)"
python3 - <<PY
import json
for i,l in enumerate(open("$O.proxy.jsonl")):
    j=json.loads(l)
    extra={k:v for k,v in j.items() if k not in("messages","tools","path","model","stream","stream_options")}
    sysm=[(m["head"],m["tail"][-60:]) for m in j.get("messages",[]) if m["role"]=="system"]
    print(i, j.get("path"), "model=",j.get("model"), "extra=",json.dumps(extra), "ntools=",len(j.get("tools",[])), "msgs=",len(j.get("messages",[])), "system=",[(h[:30],t) for h,t in sysm])
PY
echo "## message list: model of each user/assistant message and errors"
api GET /session/$SID/message | redact > $O.messages.json; python3 msgs-summary.py $O.messages.json | cut -c1-220
python3 - <<PY
import json
for m in json.load(open("$O.messages.json")):
    i=m["info"]
    if i["role"]=="user": print("user model:", i.get("model"), "variant" , i.get("variant"))
    elif i.get("error"): print("assistant error:", json.dumps(i["error"])[:300])
    else: print("assistant model:", i.get("providerID"), i.get("modelID"), "variant:", i.get("variant"))
PY
kill $SPID $PX; wait $SPID $PX 2>/dev/null
