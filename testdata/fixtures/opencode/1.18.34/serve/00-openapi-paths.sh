#!/bin/bash
# The routes of this serve (GET /doc, OpenAPI), reduced to "METHOD path operationId" and the prompt/session create body schemas.
. "$(dirname "$0")/lib.sh"; cd "$S"; O=00-openapi
serve_start $O-serve --port 0
api GET /doc > $O.doc.json
python3 - <<PY | redact > $O.txt
import json
d=json.load(open("$O.doc.json")); print("openapi", d.get("openapi"), d.get("info"))
for p,ops in sorted(d["paths"].items()):
    for m,o in ops.items():
        if isinstance(o,dict): print("%-6s %-55s %s"%(m.upper(),p,o.get("operationId","")))
def schema(n):
    s=d.get("components",{}).get("schemas",{}).get(n); print("\n#", n, json.dumps(s)[:1500] if s else "(not found)")
for p in ["/session/{sessionID}/prompt_async","/session"]:
    for m,o in d["paths"].get(p,{}).items():
        if m=="post": print("\n# POST",p,"requestBody:",json.dumps(o.get("requestBody"))[:1800])
PY
kill $SPID; wait $SPID 2>/dev/null; rm -f $O.doc.json
