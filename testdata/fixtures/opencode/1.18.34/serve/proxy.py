#!/usr/bin/env python3
# A logging pass-through to the model API (the only way to see what opencode really sends: model id, reasoning
# options, tool names). usage: proxy.py LISTEN_PORT LOG.jsonl . The upstream is https://llm.example/v1.
# It forwards the Authorization header but never logs headers; bodies are logged without message contents
# (roles + char counts only; for system messages also the first 60 and last 100 chars) and with tools reduced to names.
# PROXY_NEEDLE=text adds needle_found: whether that text is anywhere in the request body.
import http.server, json, os, sys, urllib.request
UP = "https://llm.example"
log = open(sys.argv[2], "a", buffering=1)
class H(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.0"
    def log_message(self, *a): pass
    def do_POST(self):
        n = int(self.headers.get("content-length", 0)); body = self.rfile.read(n)
        try:
            j = json.loads(body)
            rec = {k: v for k, v in j.items() if k not in ("messages", "tools")}
            rec["messages"] = [dict({"role": m.get("role"), "chars": len(json.dumps(m.get("content")))}, **({"head": str(m.get("content"))[:60], "tail": str(m.get("content"))[-100:]} if m.get("role") == "system" else {})) for m in j.get("messages", [])]
            rec["tools"] = [t.get("function", {}).get("name") for t in j.get("tools", [])]
            needle = os.environ.get("PROXY_NEEDLE")
            if needle:
                rec["needle_found"] = needle in body.decode("utf-8", "replace")
        except Exception as e:
            rec = {"unparsed": len(body), "err": str(e)}
        rec["path"] = self.path
        log.write(json.dumps(rec) + "\n")
        req = urllib.request.Request(UP + self.path, data=body, method="POST")
        for k in ("content-type", "authorization", "accept"):
            if self.headers.get(k): req.add_header(k, self.headers[k])
        try:
            r = urllib.request.urlopen(req, timeout=300)
        except urllib.error.HTTPError as e:
            self.send_response(e.code); self.end_headers(); self.wfile.write(e.read()); return
        self.send_response(r.status); self.send_header("content-type", r.headers.get("content-type", "application/json")); self.end_headers()
        while True:
            c = r.read1(4096)
            if not c: break
            self.wfile.write(c); self.wfile.flush()
http.server.ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
