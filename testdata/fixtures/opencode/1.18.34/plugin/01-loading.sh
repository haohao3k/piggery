#!/usr/bin/env bash
# Q1: how the probe plugin loads, with no model call. Four ways, each one `opencode serve` whose instance is
# started by one GET /session (project directory header). Output: $OUT/01-<way>.{probe.jsonl,files.txt,out,err}.
set -u
OUT=${OUT:-$(cd "$(dirname "$0")" && pwd)/captures}; mkdir -p "$OUT"
. "$(dirname "$0")/lib.sh"
PROBE=$S/probe/probe.js
snap() { (cd "$CFG" && find . -not -path './node_modules/*' -not -name '*.lock' | sort; echo "-- node_modules:"; ls node_modules 2>/dev/null; ls node_modules/@opencode-ai 2>/dev/null) ; }
way() { # way NAME PORT
  rm -f "$PROBE_LOG"; serve_start "$2"; api GET /session >/dev/null; sleep 3
  cp "$PROBE_LOG" "$OUT/01-$1.probe.jsonl" 2>/dev/null; snap >"$OUT/01-$1.files.txt"; stop_pid $SPID
}
rm -rf "$CFG/plugins" "$CFG/node_modules" "$CFG/package.json" "$CFG/package-lock.json" "$CFG/.gitignore" /tmp/oc/cfgdir
snap >"$OUT/01-before.files.txt"
# A: the file in $XDG_CONFIG_HOME/opencode/plugins/
mkdir -p "$CFG/plugins" && cp "$PROBE" "$CFG/plugins/probe.js"
export PROBE_LOG=/tmp/oc/probe-q1.jsonl; way A-plugins-dir 4101
rm -rf "$CFG/plugins"
# B: config `plugin: [[path, options]]` in OPENCODE_CONFIG_CONTENT (merged over the user's opencode.json, nothing edited)
export OPENCODE_CONFIG_CONTENT="{\"plugin\":[[\"file://$PROBE\",{\"opt\":1}]]}"; way B-config-content 4102
unset OPENCODE_CONFIG_CONTENT
# C: OPENCODE_CONFIG_DIR with its own plugins/ (the user's opencode.json provider stays)
mkdir -p /tmp/oc/cfgdir/plugins && cp "$PROBE" /tmp/oc/cfgdir/plugins/probe.js
export OPENCODE_CONFIG_DIR=/tmp/oc/cfgdir; way C-config-dir 4103
(cd /tmp/oc/cfgdir && find . -not -path './node_modules/*' | sort; echo "-- node_modules:"; ls node_modules node_modules/@opencode-ai 2>/dev/null) >"$OUT/01-C-config-dir.cfgdir-files.txt"
unset OPENCODE_CONFIG_DIR
# D: a project-local .opencode/plugins/ in the project directory
mkdir -p "$W/.opencode/plugins" && cp "$PROBE" "$W/.opencode/plugins/probe.js"; way D-project-dir 4104
rm -rf "$W/.opencode"
# E: the user's opencode.json has no plugin, none of the above: nothing loads (control)
way E-none 4105
for f in "$OUT"/01-*.probe.jsonl; do redact <"$f" >"$f.tmp" && mv "$f.tmp" "$f"; done
