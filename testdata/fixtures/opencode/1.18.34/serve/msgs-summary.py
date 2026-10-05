#!/usr/bin/env python3
# One line per message/part of a GET /session/:id/message capture: order, role, ids, times, finish, parts.
import json, sys
d = json.load(open(sys.argv[1]))
t0 = min(m["info"]["time"]["created"] for m in d) if d else 0
for m in d:
    i = m["info"]; tm = i.get("time", {})
    print("%s %s id=..%s parent=..%s +%dms done=%s finish=%s model=%s tokens=%s err=%s" % (
        i["role"], i.get("agent", ""), i["id"][-6:], (i.get("parentID") or "")[-6:], tm.get("created", 0) - t0,
        (tm.get("completed", 0) - t0) if tm.get("completed") else None, i.get("finish"),
        (i.get("modelID") or (i.get("model") or {}).get("modelID")), i.get("tokens"), (i.get("error") or {}).get("name")))
    for p in m["parts"]:
        t = p["type"]
        if t == "text": x = repr(p.get("text", "")[:90])
        elif t == "tool": x = "%s %s %s" % (p.get("tool"), p["state"].get("status"), json.dumps(p["state"].get("input"))[:80])
        else: x = ""
        print("    part", t, x)
