#!/bin/bash
# Q1a: how a driver learns the port, the start time, auth, the 4096 preference of --port 0.
. "$(dirname "$0")/lib.sh"; cd "$S"; O=01-start
{
echo "## A: opencode serve --port 0 (no password), 4096 free?"; lsof -iTCP:4096 -sTCP:LISTEN | wc -l | sed 's/^ *//;s/^/listeners on 4096 before: /'
serve_start $O-A.log --port 0; A=$SPID; echo "A pid=$A port=$PORT start_ms=$START_MS"; BA=$BASE
echo "## B: a second serve --port 0 while A runs"
serve_start $O-B.log --port 0; B=$SPID; echo "B pid=$B port=$PORT start_ms=$START_MS"; BB=$BASE
echo "## C: serve --port 47321 (explicit)"
serve_start $O-C.log --port 47321; C=$SPID; echo "C pid=$C port=$PORT start_ms=$START_MS"
echo "## health of each"
for b in $BA $BB $BASE; do echo "$b $(curl -sS -m 5 $b/global/health)"; done
echo "## process tree under A"; descendants $A
echo "## stop all by pid (SIGTERM)"; kill $A $B $C; sleep 1; for p in $A $B $C; do echo "$p $(alive $p)"; done
} 2>&1 | redact > $O.txt
for f in $O-*.out $O-*.err; do echo "== $f"; cat $f; done | redact > $O.logs.txt
cat $O.txt; cat $O.logs.txt
