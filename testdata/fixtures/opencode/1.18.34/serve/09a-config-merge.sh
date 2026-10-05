#!/bin/bash
# Q6: how OPENCODE_CONFIG_CONTENT / OPENCODE_PERMISSION / OPENCODE_CONFIG_DIR merge with a user's global config. No model turns.
# The "user's" config here is cfg-fixtures/user-config.json plus the provider block, in a scratch XDG dir (never ~/.config).
. "$(dirname "$0")/lib.sh"; cd "$S"; O=09a-config-merge
exec > >(redact > $O.txt) 2>&1
rm -rf /tmp/oc/cfg-fixtures /tmp/oc/xdg-cfg; cp -r cfg-fixtures /tmp/oc/cfg-fixtures; cp -r /tmp/oc/xdg /tmp/oc/xdg-cfg
python3 - <<'PY'
import json
p="/tmp/oc/xdg-cfg/config/opencode/opencode.json"; base=json.load(open(p)); user=json.load(open("/tmp/oc/cfg-fixtures/user-config.json"))
base.update({k:v for k,v in user.items() if k!="$schema"}); json.dump(base,open(p,"w"),indent=1)
PY
export XDG_CONFIG_HOME=/tmp/oc/xdg-cfg/config
MCPJ='{"type":"local","command":["true"],"enabled":false}'
show() { # show NAME : serve with the current env, print the merged config bits
  serve_start $O-$1 --port 0
  api GET /config | redact > $O.$1.config.json
  python3 - $O.$1.config.json <<PY
import json,sys
d=json.load(open(sys.argv[1]))
print("permission:", json.dumps(d.get("permission")))
print("mcp keys:", sorted((d.get("mcp") or {}).keys()))
print("plugin:", d.get("plugin"))
print("agent keys:", sorted((d.get("agent") or {}).keys()))
print("instructions:", d.get("instructions"))
print("share:", d.get("share"), " autoupdate:", d.get("autoupdate"), " lsp:", d.get("lsp"), " snapshot:", d.get("snapshot"), " model:", d.get("model"))
PY
  echo "tool ids: $(api GET /experimental/tool/ids | tr -d '\n')"
  kill $SPID; wait $SPID 2>/dev/null; echo
}
echo "## S0: the user's config only"; show S0
echo "## S1: + OPENCODE_CONFIG_CONTENT"; export OPENCODE_CONFIG_CONTENT='{"permission":"allow","mcp":{"fake":'$MCPJ'},"plugin":["file:///tmp/oc/cfg-fixtures/noop-plugin-b.js"],"agent":{"w":{"description":"w","mode":"subagent"}},"share":"disabled","lsp":false,"autoupdate":false,"snapshot":false}'; show S1
echo "## S2: CONFIG_CONTENT permission {bash:deny,question:deny} + OPENCODE_PERMISSION {bash:allow,task:deny}"; export OPENCODE_CONFIG_CONTENT='{"permission":{"bash":"deny","question":"deny"}}' OPENCODE_PERMISSION='{"bash":"allow","task":"deny"}'; show S2
echo "## S3: OPENCODE_CONFIG_DIR (opencode.json + agents/) + CONFIG_CONTENT {permission:{read:allow}}"; unset OPENCODE_PERMISSION; export OPENCODE_CONFIG_DIR=/tmp/oc/cfg-fixtures/dir OPENCODE_CONFIG_CONTENT='{"permission":{"read":"allow"}}'; show S3
echo "## S4: OPENCODE_CONFIG_DIR only (is the user's global config still read?)"; unset OPENCODE_CONFIG_CONTENT; show S4
echo "## S5: a pure worker: XDG_CONFIG_HOME pointed at an empty dir + CONFIG_CONTENT carrying the provider (user config not read at all)"
unset OPENCODE_CONFIG_DIR; mkdir -p /tmp/oc/xdg-empty; export XDG_CONFIG_HOME=/tmp/oc/xdg-empty
export OPENCODE_CONFIG_CONTENT="$(python3 -c 'import json;print(json.dumps({"permission":"allow","model":"hp/glm-5.3-flash","provider":{"hp":{"npm":"@ai-sdk/openai-compatible","name":"HP","options":{"baseURL":"https://llm.example/v1","apiKey":"{env:HP_KEY}"},"models":{"glm-5.3-flash":{"name":"glm-5.3-flash","limit":{"context":500000,"output":128000}}}}}}))')"; show S5
