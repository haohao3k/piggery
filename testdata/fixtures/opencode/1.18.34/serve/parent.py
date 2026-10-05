#!/usr/bin/env python3
# Stands in for the piggery daemon: starts `opencode serve` with stdin and stdout as pipes (like the
# local runner), writes "<serve pid> <port>" to argv[1], then sleeps until it is killed.
import os, subprocess, sys, time
env = os.environ.copy()
p = subprocess.Popen(["opencode", "serve", "--port", "0"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, cwd=sys.argv[2], env=env)
line = ""
while "listening" not in line:
    line = p.stdout.readline().decode()
port = line.strip().rsplit(":", 1)[-1]
open(sys.argv[1], "w").write("%d %s\n" % (p.pid, port))
time.sleep(10000)
