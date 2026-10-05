# Working on piggery

Notes for coding agents (and people) changing this repository, including forks. piggery is a thin
layer between coding-agent harnesses (pi, Claude Code, Codex, omp, dsh) and team layouts: a
mailbox that knows who is home, and a gate that checks every mail and spawn against the team's
layout. One Go binary, one SQLite file, one unix socket.

Read first: [README.md](README.md), then [docs/guide.md](docs/guide.md) (by task) and
[docs/reference.md](docs/reference.md) (every command and key). The templates are explained in
[manifests/README.md](manifests/README.md).

## Commands

```sh
go build ./cmd/piggery
go vet ./...
go test -race ./...
node --test extensions/pi/*.test.mjs extensions/omp/*.test.mjs extensions/dsh/*.test.mjs extensions/opencode/*.test.mjs
(cd extensions/paseo && npm ci --ignore-scripts && npm run typecheck && npm test)
go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...
```

CI runs exactly these. A change is not done until they pass.

## Where things are

| Path | What it is |
| --- | --- |
| `cmd/piggery` | The binary's entry point |
| `internal/core` | The engine: registry, mailbox, gate, board, events, timers. No I/O beyond the DB |
| `internal/store` | SQLite open and schema; `migrate_N.sql` files |
| `internal/server` | `piggery serve`: the one daemon that owns the DB and the socket |
| `internal/proto` | The socket wire format (JSON lines), shared by server and clients |
| `internal/cli` | The `piggery <cmd>` client, `top`, `setup <harness>`, hooks and `piggery mcp` |
| `internal/driver/local` | Headless workers: the process runner plus one codec per harness |
| `internal/view` | What `top`, `ps` and the Paseo plugin show |
| `extensions/` | What runs inside a harness: pi, omp and dsh extensions, the Paseo plugin |
| `manifests/` | Built-in templates (`*.yaml`) and their role prompts, embedded in the binary |
| `testdata/fixtures/` | Real captures of each harness, by version, replayed by the tests |

## Rules that keep the design intact

These are the mistakes that are easy to make and expensive to undo.

1. **No harness names in `core`.** The engine never branches on "pi", "claude" and so on. A
   harness goes through `core.RuntimeDriver` (`internal/core/runtime.go`) for workers and through
   the adapter socket protocol for sessions; capabilities (`wake`, `steer`, `abort`, …) are
   declared, and core reads only those. Do not use Go's `plugin` package.
2. **Mail is a black box.** Core never looks inside `body` or `kind`. It acts only on reserved
   addresses (`notify`, `board`, `engine`), envelope fields (`to`, `reply_to`, `op`, `target`,
   …) and action or tool names. If you want behaviour keyed on what a message says, it belongs in
   a role prompt, not in core.
3. **Ack only on a matching completion.** A delivered batch is acknowledged only when the harness
   reports the end of that batch's turn for the participant's current run. Never ack because a
   participant looks idle, because a sequence number moved, or because a retry happened. Lost acks
   are recoverable; wrong acks lose mail silently.
4. **State tables are the truth.** Events are written only for decisions (denied, held, released,
   …) and team or worker lifecycle (spawned, stopped, exited, …), in the same transaction as the
   state change. No per-message or per-turn events; nothing is rebuilt from events.
5. **The gate is code; prompts are advice.** Who may spawn whom, who may write to whom and the
   limits live in the manifest and are enforced in core. Do not move enforcement into prompts, and
   do not add core rules for what a prompt can ask for.
6. **No new model tool when `send` or `inbox` can say it.** The tools models see (`send`,
   `inbox`, `who`, `agent`) are defined once in `extensions/pi/tools.json`; every harness builds
   them from that file. A new capability is usually a new address, a `view`, or an `agent`
   action.
7. **Schema changes are new migrations.** Add `internal/store/migrate_N.sql`; never edit an old
   one. Upstream adds migrations too, so a fork-only migration will collide by number: send
   schema changes upstream instead of carrying them.
8. **Installed integrations carry a version.** When what `setup <harness>` installs changes (an
   extension file, a hook, a config entry), bump that harness's integer in
   `internal/driver/local/integration.go`. A rebuild that installs the same thing keeps it.
9. **Harness behaviour needs a capture.** Before relying on how a harness behaves (an event's
   order, a field, what happens on abort), capture it from the real harness, commit it under
   `testdata/fixtures/<harness>-<version>/`, and test against the capture. Record the version in
   the harness profile's `tested_versions`.

## Adding a harness

One file per concern, one registration line each, nothing in `core`:

- a codec in `internal/driver/local/<harness>.go` (commands to the harness, its output to
  standard records) if piggery starts its workers;
- an adapter for interactive sessions: an extension under `extensions/<harness>/`, or hooks plus
  `piggery mcp` for harnesses without an extension API;
- `internal/cli/setup_<harness>.go` for `piggery setup <harness>`, `remove` and the doctor check;
- a profile `~/.piggery/harness/<harness>.json` (cmd, args, model, thinking, blacklist,
  tested_versions);
- captures in `testdata/fixtures/` and contract tests that replay them.

## Tests

- One test per invariant or distinct risk. No permutation tables of the same path, no tests of
  formatting, getters or wiring, no mocks where the real call is cheap.
- A bug you reproduce gets a regression test that fails before the fix.
- Harness behaviour is tested by replaying committed captures, not by calling a model.
- Live runs with real models are manual. Use a cheap model and a private `HOME`
  (`HOME=$(mktemp -d) piggery setup`), so the daemon, its DB and the harness's config are not the
  user's. Stop the processes you started by pid, never by name: the user's own daemon has the same
  command line.

## Docs and changelog

- A change users can see updates [docs/guide.md](docs/guide.md) (how to do things) or
  [docs/reference.md](docs/reference.md) (every command and key), not both with the same text.
- Add a line under the next version in [CHANGELOG.md](CHANGELOG.md).
- Keep comments and docs to what the code does and why; no history of how it got there.

## Forks and pull requests

- Keep fork-only tooling (local build scripts, machine-specific installs) out of shared paths such
  as `install.sh`, `piggery update` and the CI workflows, so upstream releases still merge.
- Prefer small pull requests against `main`, one concern each, with the CI commands above passing.
- Template changes are data: a new layout is a new `manifests/<name>.yaml` plus its prompts, not a
  core change. Hard-coded models in a template tie it to one account; prefer `inherit` and say in
  the docs which models fit.

# Piggery fork development

## Repository ownership

- The working remote is `https://github.com/haohao3k/piggery.git`. `origin`, the push default,
  and the GitHub CLI default repository must point to this fork. Use `codex/` branches.
- `upstream` is `sting8k/piggery`, for fetching and comparison only. Never push there. Preserve
  its disabled push URL. Do not merge, publish a release, or push to another repository without
  the Human's authorization.
- Upstream is the source of truth for shared Piggery behavior. Adopt its accepted fixes rather
  than maintaining competing fork implementations; keep the runtime and schema aligned. Convert known old fork data once through the guarded
  development installer instead of retaining fork-only migrations or core authentication paths.
- Upstream issues and pull requests contain only generally applicable upstream problems and fixes.
  Prepare PRs from a clean upstream base. Never include this fork's local build/install tooling,
  custom templates, model routing, machine paths, private state or fork-only policy in an upstream PR.
  Verify the complete diff against upstream before submission; use synthetic public reproductions.
- `piggery update` and `install.sh` download releases only from `haohao3k/piggery`. They never
  build a checkout, depend on a local receipt, or fall back to `sting8k/piggery`. A push to main
  is not a release: publish binaries and checksums through the fork's release workflow separately.
- Preserve unrelated work and existing templates, profiles, sessions, messages and credentials.
  A shared changing scope has one write owner; coordinate before editing another owner's files.
- Use Semble for exploratory code discovery, then read the returned locations directly. Use
  literal `rg` searches when all occurrences are needed.

## Hard constraint: a change is not done until the local runtime matches it

For every Piggery source, CLI, adapter, embedded prompt/template/skill, integration asset, or
build/install-script change:

1. Run checks appropriate to the changed behavior. Build from this checkout, including its
   current embedded assets. Use `./scripts/local-dev.sh build`; a release download or
   `go install github.com/sting8k/piggery/cmd/piggery@latest` is not a substitute.
2. Run `./scripts/local-dev.sh apply` to install the candidate, refresh already-installed
   integration assets and restart the same Piggery home. This workflow is authorized by the
   Human's local-development direction; do not ask for another approval for a routine safe apply.
3. Run `./scripts/local-dev.sh check`. Completion requires current source fingerprint, installed
   CLI identity/hash and live daemon version to match, plus successful asset refresh evidence.
   A successful compile or an edited template alone is not completion.
4. Rebuild/apply again if source or assets changed after the candidate was built. Keep the final
   check result with the handback, including source revision, version and any remaining limits.

`apply` must obtain a fresh authoritative runtime readback before replacing the executable or
restarting. Busy/starting/awaiting-permission or unknown states block activation. The exact
invoking solo session may be identified with `--caller <participant-id>`; this must never exempt
another session or a headless worker. Idle workers are stopped by Piggery's normal graceful
restart, their exits/sessions are retained, and the receipt must list them. Do not automatically
resume them or start provider calls as part of installation.

If activation is blocked, finish the source/tests/candidate and report **BLOCKED activation**
with the exact state. Do not claim the installed runtime is current, weaken the gate, or kill
busy agents to satisfy this rule. Recheck when the blocking state changes.

The stable executable is the local installation on PATH (normally `~/.local/bin/piggery`).
Keep the existing Piggery home and configuration; do not reset profiles, overwrite custom
project templates, print tokens, or copy the database into the repository. `setup --refresh`
refreshes installed assets even when their integration-version integer did not change.
Custom template conflicts must remain visible instead of being silently overwritten.

Existing long-lived harness processes may retain loaded code or instructions until they reconnect
or reload. Report that boundary honestly; never equate installing new bytes with reloading every
running model context. New sessions must resolve the local executable/assets. Do not restart an
unrelated host application merely to reload an integration.

`scripts/local-dev.sh` is an explicit developer build/apply/check tool, separate from the shipped
release updater. Its receipts verify test deployments only; they do not bind `piggery update` to
this checkout. `piggery update --check` checks the latest fork release, and `--force` permits
replacing a development build or reinstalling a release. Use the script's `check` for source/runtime
fingerprint verification and its guarded `apply` for development activation.
Documentation-only changes do not require a daemon
restart unless the installed instruction/assets or the build fingerprint are affected; they
still require applicable validation and the same honest handback.

See [the local development workflow](docs/local-development.md) for commands and recovery.
