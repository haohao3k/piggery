# Shared helpers for the opencode capture capture (b). Source it; it needs /tmp/oc (a copy of /tmp/oc/xdg, see README).
# Never prints HP_KEY: every capture goes through `redact`.
S=${S:-$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)}
source /tmp/oc/env.sh
unset OPENCODE_SERVER_PASSWORD OPENCODE_SERVER_USERNAME OPENCODE_CONFIG OPENCODE_CONFIG_DIR OPENCODE_CONFIG_CONTENT OPENCODE_PERMISSION
W=/tmp/oc/work
PIDS=/tmp/oc/pids          # every pid this capture started, one per line (stopped by pid at the end)
mkdir -p "$W" ; touch "$PIDS"
now_ms() { python3 -c 'import time;print(int(time.time()*1000))'; }
# redact: stdin -> stdout, the key and any "Basic ..." header value removed.
redact() { python3 -c 'import os,sys
k=os.environ.get("HP_KEY","")
d=sys.stdin.read()
if k: d=d.replace(k,"<HP_KEY>")
sys.stdout.write(d)'; }
# serve_start LOGPREFIX [serve args...]: starts opencode serve in $W, stdout->LOGPREFIX.out, stderr->LOGPREFIX.err,
# sets SPID and PORT (parsed from the stdout line), START_MS (ms until that line appeared).
serve_start() {
  local pre=$1; shift; case $pre in /*) ;; *) pre=$PWD/$pre;; esac
  local t0; t0=$(now_ms)
  # sigdfl: bash starts background jobs with SIGINT ignored (inherited over exec); reset it so SIGINT tests are real.
  ( cd "$W" && exec python3 -c 'import os,signal,sys;signal.signal(signal.SIGINT,signal.SIG_DFL);os.execvp("opencode",["opencode","serve"]+sys.argv[1:])' "$@" >"$pre.out" 2>"$pre.err" </dev/null ) &
  SPID=$!; echo $SPID >>"$PIDS"
  for i in $(seq 1 600); do
    grep -q 'listening on' "$pre.out" 2>/dev/null && break
    kill -0 $SPID 2>/dev/null || break
    sleep 0.05
  done
  START_MS=$(( $(now_ms) - t0 ))
  PORT=$(sed -n 's/.*listening on http:\/\/[^:]*:\([0-9]*\).*/\1/p' "$pre.out" | head -1)
  BASE=http://127.0.0.1:$PORT
}
# api METHOD PATH [json-body]: curl with optional basic auth ($AUTH = "user:pass"), the project dir header.
api() {
  local m=$1 p=$2 b=${3-}
  local a=(); [ -n "${AUTH-}" ] && a=(-u "$AUTH")
  if [ -n "$b" ]; then curl -sS -m ${TMO:-120} "${a[@]}" -X "$m" -H 'content-type: application/json' -H "x-opencode-directory: $W" -d "$b" "$BASE$p"
  else curl -sS -m ${TMO:-120} "${a[@]}" -X "$m" -H "x-opencode-directory: $W" "$BASE$p"; fi
}
# descendants PID: ps lines of all descendants of PID (pid ppid pgid command).
descendants() {
  python3 - "$1" <<'PY'
import subprocess,sys
root=int(sys.argv[1])
rows=[l.split(None,3) for l in subprocess.run(["ps","-axo","pid=,ppid=,pgid=,command="],capture_output=True,text=True).stdout.splitlines()]
rows=[(int(a),int(b),int(c),d) for a,b,c,d in (r+[""]*(4-len(r)) for r in rows)]
kids={}
for p,pp,g,c in rows: kids.setdefault(pp,[]).append((p,pp,g,c))
out=[];st=[root]
while st:
    x=st.pop()
    for r in kids.get(x,[]):
        out.append(r);st.append(r[0])
for r in out: print("%d ppid=%d pgid=%d %s"%(r[0],r[1],r[2],r[3][:150]))
PY
}
alive() { kill -0 "$1" 2>/dev/null && echo alive || echo dead; }
# sse_start OUT: SSE capture in the background (pid in SSEPID); sse_stop kills it by pid.
sse_start() { OC_DIR=$W python3 "$S/sse.py" "$BASE" "$1" ${AUTH-} 2>"$1.err" & SSEPID=$!; echo $SSEPID >>"$PIDS"; sleep 1; }
sse_stop() { kill $SSEPID 2>/dev/null; wait $SSEPID 2>/dev/null; }
# sid: id of a session JSON on stdin.
sid() { python3 -c 'import json,sys;print(json.load(sys.stdin)["id"])'; }
# wait_idle SID [secs]: waits until the session shows as busy in /session/status (up to 8 s: right after a prompt on a fresh
# instance it may not be busy yet), then until it is absent (idle); prints ms from the call to idle.
wait_idle() { local t0 st; t0=$(now_ms)
  for i in $(seq 1 32); do api GET /session/status | python3 -c 'import json,sys;sys.exit(0 if "'$1'" in json.load(sys.stdin) else 1)' && break; sleep 0.25; done
  for i in $(seq 1 $(( ${2:-120} * 4 ))); do
    api GET /session/status | python3 -c 'import json,sys;sys.exit(0 if "'$1'" in json.load(sys.stdin) else 1)' || { echo $(( $(now_ms)-t0 )); return 0; }; sleep 0.25; done; echo timeout; }
