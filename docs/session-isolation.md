# Codex session isolation

This fork follows upstream v0.9.0 and [PR #6](https://github.com/sting8k/piggery/pull/6),
which supersedes our [PR #5](https://github.com/sting8k/piggery/pull/5) for
[issue #4](https://github.com/sting8k/piggery/issues/4).

## Current contract

Under a shared Codex app-server, the adapter names each host as
`codex:<pid>:<start>/<session id>`. Hooks supply `session_id`; MCP tool calls supply
`_meta.sessionId`. A call without that metadata is refused. One MCP child maintains a separate
server and daemon connection for each thread, so mail and wake delivery remain available to
multiple threads concurrently. The socket peer check validates the process portion of the host.
Core authenticates hosts using the upstream contract, without a Codex-specific authentication path.

A standalone Codex TUI and Claude keep a participant across `/clear`, as upstream does.
The previous fork-only `session.support`, host/ref/cwd authentication and `_meta.threadId`
contract are removed. Reconnect old adapters after installing the matching daemon and CLI;
their cached connections and instructions do not change when the executable is replaced.

## Historical fork data

The runtime and store use upstream schema 27. The old fork-only migration 23 added a
`binding_quarantined` column to flag ambiguous historical identities; it was not an upstream
version or a newer Piggery release.

For an existing fork-23 installation, guarded `scripts/local-dev.sh apply` stops the daemon only
when idle, acquires its exclusive lock, validates the exact known schema and writes a complete
SQLite backup under `~/.piggery/backups/fork23-to-upstream22-*.db`. It requires every flagged
participant to be a gone interactive session. Those rows receive upstream's `left_at` marker,
lose their old host and token, and keep their identity references and history. The script drops
the fork column, verifies the resulting schema against upstream migrations, and sets version 22
in the same transaction. The runtime then applies upstream migrations through 27. A failed binary
installation rolls the conversion transaction back. Native upstream schemas 23–27 are recognized
by their schema shape and never run through the historical fork conversion.

Messages, acknowledgements, aliases and unflagged participants remain unchanged. Reconnecting
an ambiguous old session creates a fresh participant; no old mail is reassigned. Unknown schema
variants or active flagged participants block conversion. This one-time developer conversion
is separate from the release updater and introduces no fork migration into the Go runtime.

## Installation and verification

Use the guarded [local workflow](local-development.md): test, build, apply only after an
authoritative idle readback, then verify the installed CLI, daemon and refreshed assets.
Existing fork-23 homes must complete the guarded conversion before installing a release.
Never change the version metadata alone.
Keep the existing Piggery home and backups. Activation must wait while any other session or worker
is busy or awaiting permission. After activation, reconnect Codex's app-server/MCP processes at a
safe time; an installed candidate alone does not reload running sessions.

The upstream author reports live verification on Codex 0.160.0 with a shared app-server and two
remote TUIs. Local automated tests cover adapter identity, missing metadata, the standard TUI and
Claude behavior, and safe retirement of historical ambiguous bindings. They are not a live multi-chat test.
