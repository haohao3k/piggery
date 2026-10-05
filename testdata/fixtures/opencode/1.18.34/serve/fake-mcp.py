#!/usr/bin/env python3
# A minimal stdio MCP server (one tool, "ping") used to see whether opencode leaves MCP children behind.
# It writes its pid to $FAKE_MCP_PID_FILE and logs its stdin EOF / SIGTERM there too.
import json, os, signal, sys
pf = os.environ.get("FAKE_MCP_PID_FILE")
def note(s):
    if pf:
        open(pf + ".log", "a").write(s + "\n")
if pf:
    open(pf, "w").write(str(os.getpid()))
signal.signal(signal.SIGTERM, lambda *a: (note("got SIGTERM"), sys.exit(0)))
for line in sys.stdin:
    try: m = json.loads(line)
    except Exception: continue
    if "id" not in m: continue
    meth = m.get("method")
    if meth == "initialize":
        r = {"protocolVersion": m["params"].get("protocolVersion", "2024-11-05"), "capabilities": {"tools": {}}, "serverInfo": {"name": "fake", "version": "0"}}
    elif meth == "tools/list":
        r = {"tools": [{"name": "ping", "description": "returns pong", "inputSchema": {"type": "object", "properties": {}}}]}
    elif meth == "tools/call":
        r = {"content": [{"type": "text", "text": "pong"}]}
    else:
        r = {}
    sys.stdout.write(json.dumps({"jsonrpc": "2.0", "id": m["id"], "result": r}) + "\n"); sys.stdout.flush()
note("stdin EOF")
