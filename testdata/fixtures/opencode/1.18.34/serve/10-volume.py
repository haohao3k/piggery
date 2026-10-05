#!/usr/bin/env python3
# Q7: what an SSE tail would cost and what a compact log needs. Reads the SSE captures of 03/04/05 (no model call).
# usage: 10-volume.py > 10-volume.txt
import json, collections
files = ["03-prompt.sse.jsonl", "04-steer.sse.jsonl", "05-abort.sse.jsonl"]
for f in files:
    cnt = collections.Counter(); size = collections.Counter(); tot = 0
    for l in open(f):
        d = json.loads(l).get("data")
        if not isinstance(d, dict): continue
        p = d.get("properties", {})
        k = d["type"]
        if k == "message.part.updated": k += ":" + (p.get("part") or {}).get("type", "?")
        n = len(json.dumps(d)); cnt[k] += 1; size[k] += n; tot += n
    print("== %s: %d events, %d bytes" % (f, sum(cnt.values()), tot))
    for k, c in cnt.most_common(): print("  %-34s %4d events %7d bytes" % (k, c, size[k]))
print()
print("== the events a compact log needs (all fields below are in the capture; field paths in properties.*)")
print("   assistant text, final:   message.part.updated  part.type=text   part.time.end set  (part.text is the full text; deltas are redundant)")
print("   reasoning (optional):    message.part.updated  part.type=reasoning part.time.end set")
print("   tool call:               message.part.updated  part.type=tool  part.tool, part.state.status pending|running|completed|error, part.state.input (+ .output / .error when done)")
print("   usage/ctx per step:      message.part.updated  part.type=step-finish  part.tokens {total,input,output,reasoning,cache{read,write}}, part.cost, part.reason")
print("   usage per assistant msg: message.updated       info.role=assistant info.tokens (same shape), info.finish, info.time.completed, info.modelID/providerID, info.error")
print("   turn start/end:          session.status properties.status.type busy|idle (a busy is repeated; take the last), session.idle (alias)")
print("   abort/failure:           session.error properties.error.name (MessageAbortedError, ...)")
print("   a steered-in user msg:   message.updated info.role=user (+ message.part.updated part.type=text) while the session is busy")
print("   skip: message.part.delta (one per token), plugin.added / catalog.updated / reference.updated / integration.updated (instance boot), session.updated, session.diff, server.heartbeat (every 10 s)")
print()
# a worked example: the compact records derived from 04-steer.sse.jsonl
print("== compact records derived from 04-steer.sse.jsonl (t = ms after the first event):")
t0 = None
for l in open("04-steer.sse.jsonl"):
    r = json.loads(l); d = r.get("data")
    if not isinstance(d, dict): continue
    t0 = t0 or r["t"]; p = d.get("properties", {}); t = d["type"]; out = None
    if t == "message.part.updated":
        part = p["part"]; ty = part["type"]
        if ty == "text" and (part.get("time") or {}).get("end"): out = ("assistant_text" if True else "", part["text"])
        elif ty == "tool" and part["state"]["status"] in ("running", "completed", "error"): out = ("tool", part["tool"], part["state"]["status"], json.dumps(part["state"].get("input"))[:70])
        elif ty == "step-finish": out = ("usage", part["tokens"], part["reason"])
    elif t == "message.updated" and p["info"]["role"] == "user" and False: out = ("user",)
    elif t == "session.status": out = ("status", p["status"]["type"])
    if out: print("  %6d %s" % (r["t"] - t0, out))
