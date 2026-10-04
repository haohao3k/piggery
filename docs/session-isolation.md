# Codex session isolation and upgrade recovery

Codex Desktop can host unrelated conversations in one app-server process. Its PID and start
time prove transport ancestry; they do not identify a conversation. Two chats in one directory
also remain two independent sessions.

Interactive Codex requests bind the host, exact thread reference and canonical directory.
Hooks supply `session_id` and `cwd`; MCP calls supply the harness's thread metadata. A request
without a thread reference cannot fall back to the newest participant under that process.
Before joining or sending a hook event, the Codex adapter probes the daemon's read-only
`session.support` capability. An old daemon that ignores the new authentication fields is
refused before it can mutate a binding.
The daemon refuses a different directory before executing a tool or returning mail. Worker
processes continue using their explicit participant credentials and run ownership checks.

Codex MCP discovery starts without a verified thread reference. The server exposes a generic
tool catalog and asks for one `who` call; the first exact call binds the thread, returns its
role card and enables MCP wake routing. Until then it cannot queue wakes for a guessed thread.
Hooks still carry their own explicit session reference. After `/clear`, a new thread reference
binds independently and receives a fresh role card.

The adapter keeps one current binding per MCP child and serializes reference changes. The
observed Codex 0.160 installation retains separate MCP children, but its per-call metadata
does not promise one child per thread. A host that multiplexes simultaneous threads through
one child needs separate children or a future per-thread connection map for continuous wake
availability. Threads never inherit each other's participant or project through a rebind.

The same thread may resume under a new host process in the same directory. A different thread
creates a separate participant even when its source is `clear`. Codex does not provide enough
verified predecessor information to infer lineage from that flag alone. Claude's established
single-session process and `/clear` behavior are unchanged.

## Return addresses

`who` includes the invoking participant's stable `id=` in its own row. Capture that identifier
and the verified project root in each review launch brief. A delegated coordinator sends its
handback to that exact identifier; Piggery already accepts participant IDs as send addresses
and still enforces gate-to-gate authorization. Display names are useful labels, but a template
must not retain a previous caller's address. An unavailable endpoint is a failed delivery to
record beside the durable report, never permission to pick another chat or project.

## Existing installations

Schema 23 quarantines ambiguous legacy interactive Codex bindings: rows whose current thread
differs from the original reference, or which accumulated other thread aliases. They cannot
authenticate or be automatically reclaimed through an alias. A reconnect creates independent
participants for the real threads. Some old `/clear` aliases may have been legitimate; without
evidence the migration does not guess which conversation owns a shared row.

All participants, aliases, teams, messages, acknowledgements and other history remain in the
database. Mail addressed to an old participant is not reassigned or replayed to a guessed owner.
`piggery doctor` reports quarantined IDs for deliberate recovery. Existing team ownership and
unfinished work must be inspected explicitly before moving a return route. In particular,
quarantine is not a request to restart workers, close teams or resume a development lane.

The store makes its normal pre-migration backup before applying schema 23. Use the guarded
[local workflow](local-development.md): build and test first, apply only with an authoritative
idle readback, then check the installed binary, daemon and refreshed integrations. Do not run
an older binary against the migrated live database. Preserve the pre-upgrade backup if a
deliberate rollback is needed.

Long-lived MCP children retain their loaded executable. Reconnect the affected Codex chats
after activation. Host-only requests from an old adapter fail closed against the new daemon;
installing a binary does not rewrite instructions already loaded in a model's context.
