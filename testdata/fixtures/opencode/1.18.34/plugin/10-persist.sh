#!/usr/bin/env bash
# Q10: what is on disk and readable without the daemon, from the private data dir ($XDG_DATA_HOME/opencode/opencode.db),
# read-only, while the TUI is still running (WAL). Output: captures/10-*.txt. No model call.
set -u
OUT=${OUT:-$(cd "$(dirname "$0")" && pwd)/captures}; mkdir -p "$OUT"
. "$(dirname "$0")/lib.sh"
DB=$XDG_DATA_HOME/opencode/opencode.db
Q() { sqlite3 -readonly -header -column "$DB" "$1" 2>&1; }
{ echo "# files"; ls -la "$XDG_DATA_HOME/opencode/" | awk '{print $5, $9}'; echo; echo "# tables"; sqlite3 -readonly "$DB" .tables; } >"$OUT/10-db-tables.txt" 2>&1
sqlite3 -readonly "$DB" ".schema session" ".schema message" ".schema part" ".schema permission" ".schema todo" >"$OUT/10-db-schema.txt" 2>&1
Q "select id, parent_id, directory, title, time_created, time_updated, time_archived from session order by time_created" >"$OUT/10-sessions.txt"
# turns = user messages, ctx = the last assistant message's tokens, tail = the last text parts: all from message.data / part.data JSON
Q "select s.id, (select count(*) from message m where m.session_id=s.id and json_extract(m.data,'\$.role')='user') as turns,
 (select count(*) from message m where m.session_id=s.id and json_extract(m.data,'\$.role')='assistant') as assistant_msgs,
 (select json_extract(m.data,'\$.tokens.total') from message m where m.session_id=s.id and json_extract(m.data,'\$.role')='assistant' order by m.time_created desc limit 1) as last_tokens_total,
 (select json_extract(m.data,'\$.modelID')||' '||coalesce(json_extract(m.data,'\$.variant'),'-') from message m where m.session_id=s.id and json_extract(m.data,'\$.role')='assistant' order by m.time_created desc limit 1) as last_model
 from session s order by s.time_created" >"$OUT/10-turns-ctx.txt"
Q "select p.session_id, json_extract(p.data,'\$.type') as type, substr(coalesce(json_extract(p.data,'\$.text'), json_extract(p.data,'\$.tool')),1,70) as text from part p where json_extract(p.data,'\$.type') in ('text','tool') order by p.time_created desc limit 12" >"$OUT/10-tail.txt"
Q "select m.id, json_extract(m.data,'\$.role') role, json_extract(m.data,'\$.finish') finish, json_extract(m.data,'\$.error.name') err, json_extract(m.data,'\$.parentID') parent from message m order by m.time_created desc limit 8" >"$OUT/10-last-messages.txt"
# the CLI readers (no model)
( cd "$W" && opencode session list 2>&1 | head -12 ) >"$OUT/10-cli-session-list.txt"
SID=$(sqlite3 -readonly "$DB" "select id from session where parent_id is null order by time_created limit 1")
( cd "$W" && opencode export "$SID" 2>/dev/null | head -c 1500 ) | redact >"$OUT/10-cli-export-head.txt"
( cd "$W" && opencode stats 2>&1 | head -30 ) >"$OUT/10-cli-stats.txt"
