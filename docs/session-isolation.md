# Codex session isolation

This fork follows upstream v0.7.1 and [PR #6](https://github.com/sting8k/piggery/pull/6),
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

The fork already deployed schema 23 before upstream chose its adapter fix. Keep that migration
unchanged and retain its quarantine column, lookup exclusions, token refusal, wake suppression
and doctor output. This is storage compatibility for existing fork data, not another Codex routing
implementation. Upstream v0.7.1 still uses schema 22; its stock binary cannot open a fork schema-23
home. Future upstream migrations must be reconciled explicitly with this fork's migration history.

Quarantined participants, aliases, messages and acknowledgements remain stored. They cannot be
rebound or used to authenticate, and reconnecting creates a fresh participant without replaying
old mail. Non-quarantined threads can resume with the upstream thread host. Existing historical
identity mistakes are not automatically repaired. `piggery doctor` reports quarantine candidates
for deliberate recovery; it does not infer the intended recipient of old messages.

## Installation and verification

Use the guarded [local workflow](local-development.md): test, build, apply only after an
authoritative idle readback, then verify the installed CLI, daemon and refreshed assets.
Do not run the upstream release installer over this fork or downgrade its database metadata.
Keep the existing Piggery home and backups. Activation must wait while any other session or worker
is busy or awaiting permission. After activation, reconnect Codex's app-server/MCP processes at a
safe time; an installed candidate alone does not reload running sessions.

The upstream author reports live verification on Codex 0.160.0 with a shared app-server and two
remote TUIs. Local automated tests cover adapter identity, missing metadata, the standard TUI and
Claude behavior, and preservation of historical quarantine. They are not a live multi-chat test.
