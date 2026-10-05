#!/usr/bin/env python3
# Reads GET /event (SSE) until killed; writes one JSON line per event: {"t": ms epoch, "data": <parsed data>}.
# usage: sse.py BASE OUT.jsonl [user:pass]   (the x-opencode-directory header comes from $OC_DIR)
import base64, json, os, sys, time, urllib.request
base, out = sys.argv[1], sys.argv[2]
req = urllib.request.Request(base + "/event")
req.add_header("x-opencode-directory", os.environ.get("OC_DIR", ""))
if len(sys.argv) > 3: req.add_header("authorization", "Basic " + base64.b64encode(sys.argv[3].encode()).decode())
f = open(out, "w", buffering=1)
with urllib.request.urlopen(req) as r:
    f.write(json.dumps({"t": int(time.time()*1000), "http": r.status, "content-type": r.headers.get("content-type")}) + "\n")
    for raw in r:
        line = raw.decode().rstrip("\n")
        if line.startswith("data:"):
            try: d = json.loads(line[5:].strip())
            except Exception: d = line[5:]
            f.write(json.dumps({"t": int(time.time()*1000), "data": d}) + "\n")
