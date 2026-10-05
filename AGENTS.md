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
