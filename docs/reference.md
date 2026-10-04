# Piggery reference

Dry lists. How to use piggery, by task: [guide.md](guide.md). Files in `~/.piggery` that `piggery
setup` writes carry a comment on each key; where one does, this page gives one line.

## Commands

`piggery --help` lists them by task and `piggery <command> --help` shows one. These commands run as
you (admin, read from `~/.piggery/admin.token`) and start the daemon if it is not running.

| Command | What it does |
|---|---|
| `setup [pi\|claude\|codex\|omp\|dsh\|paseo]` | Add piggery to a harness (alone: write missing profiles, templates and config keys, and show where each harness stands) |
| `setup --outdated` | Update every installed integration that is outdated (pi, omp, dsh, claude, codex, paseo): runs `setup <harness>` for each and says what to do after (reopen Codex sessions, reload the Paseo app); not installed ones are untouched; `piggery integrations are up to date` when nothing is; exit 1 if an update failed (the others still run) |
| `setup notify [add\|remove <desktop\|herdr\|ntfy:TOPIC>] [--force]` | The notify hooks in `hooks/notify.d/`: alone, lists them and what each target needs on PATH (`jq` for all; `osascript` or `terminal-notifier`, `notify-send`, `herdr`, `curl`); `add` writes one script (mode 0700, a `# written by piggery setup notify add <target>` marker on its second line; `ntfy:<topic>` is the file `ntfy-<topic>`); `remove` deletes only a file with that marker. A file you wrote is left alone (exit 1); `add --force` replaces it |
| `setup --refresh` | Refresh already-installed integration assets even at the same integration version, and safely unpack built-in templates; preserve custom template overrides and report their names; no new harness integration is enabled; mutually exclusive with `--outdated` and harness arguments |
| `setup remove <harness>` | Take out exactly what `setup` added; `--ext PATH` (setup pi) uses a checkout's extension |
| `skills [three-review]` | Print the agent guide, or the shared three-arm context-preparation and review playbook; does not start a daemon, team or model |
| `team up <template\|path.yaml> [--cwd D] [--name N]` | Start a team from a template in `~/.piggery/templates`, or a manifest file |
| `team down <team>` | Close a team: workers stopped, nothing acked |
| `template new <name> [--from <built-in>]` | Copy a built-in (default `p2p`) to `~/.piggery/templates/<name>` |
| `ps [--json\|--view]` | Daemon, teams, members, solo sessions and pending mail, once; `--view` prints what `top` shows (rows, header counts, events, the latest notices as `notices`, the update notice as `daemon.update`, each row's actions and Overview) as one JSON document with a `version` field, for the Paseo plugin |
| `top` | The same, live, with context, turns and the latest events and notices (Notices at the bottom left once there is a notice, Events on the right, split where the list and the Overview are; stacked in a narrow window); its header says `vX available: piggery update` when a newer release is out; `↑/↓` select (`PgUp`/`PgDn`, `Home`/`End` in a long list, which scrolls under its header; `↑ N`/`↓ N` on the border count hidden lines), `enter` opens or closes (a member's details, a team's members, a team's gone-members line; kept for the next `top`), `t` switches Overview and Tail, `e` opens the events list (folded to the latest event by default; kept for the next `top`), `n` opens or closes the Notices box (the latest notices; folded to the newest one by default, kept for the next `top`, like `e`), `x` kills the selected headless worker (asks once); a click on the model (blue, `▾`) in a live headless worker's Overview, or `M`, opens a picker for its model and thinking level (`enter` or a double-click applies, `esc` closes) |
| `tail <worker> [-n N] [-f] [--team T]` | A worker's log or a session's transcript, readable; `--json` raw; `--view` the readable lines with their kind, for the Paseo plugin |
| `log [--after SEQ] [--team T] [--limit N]` | Decisions and lifecycle events |
| `abort <x> [--team T]` | Cancel x's current turn; it stays alive |
| `kill <worker> [--team T]` (short: `x`) | Kill a worker: SIGTERM so its harness cleans up, then after up to 2s SIGKILL for it and every process left in its tree; `x` on the selected worker in `top` asks `kill <name>? y/n` and does the same |
| `resume <worker> [--team T]` | Start a stopped worker again in its session, with its model, thinking level and harness |
| `model <worker> [<provider/model>] [--thinking L] [--team T]` | Change a worker's model or thinking level now, or at its resume |
| `release <msg_id>` | Deliver a message held by a limit |
| `why <from> <to> [--team T]` | Every gate check for a send; nothing is sent |
| `gc --closed-before D [--dry-run]` | Archive, verify and delete closed teams and gone solo sessions |
| `archive show <file> [--table T]` | Print a gc archive (local, no daemon) |
| `check [--json]` | Local, no daemon, changes nothing: reads `config.yaml`, `harness/*.json`, every template and the `prompts` entries with the loaders the daemon and `team up` use. An error line for what they refuse, then `warning:` lines for what they skip or ignore (a prompt left out, a `to: notify` line, an old `hooks/notify`); exit 1 when any error. `--json`: `{"errors": [], "warnings": []}` |
| `doctor` | Findings about the daemon's state; exit 1 when any |
| `shutdown` / `restart` | Stop the daemon (workers stopped), or stop and start it again from this binary |
| `update [--check] [--force] [--caller ID]` | Managed local builds: build/apply/check the owning checkout's source and embedded assets, without downloading releases; `--check` is read-only and fails on drift. `--caller` permits only the exact invoking solo during safe activation, and `--force` cannot bypass the gate. Bootstrap or repair an unbound source build with `scripts/local-dev.sh apply`. Release builds: install the latest fork release; `--force` reinstalls; `--caller` is unavailable. |

`ps` and `top` show a line such as `outdated: codex (v0 < v1): piggery setup --outdated` in their header
when a claude, codex or paseo install is outdated (`ps --json` has the same list as `outdated`).

Flags on every command: `-a/--admin`, `--json` (raw JSON result), `--no-start` (fail instead of
starting the daemon), `-h/--help`; `-v/--version` on `piggery`. In a shell where `PIGGERY_ID` and
`PIGGERY_TOKEN` are set (an agent's), pass `--admin` to act as you. The agents' commands (`send`,
`inbox`, `who`, `board`, `watch`, `agent`, `join`) are hidden from help; `piggery skills` describes
them.

The model sees the tools with a `piggery_` prefix in pi, omp and dsh (`piggery_send`) and as
`mcp__piggery__send` in Claude Code and Codex; manifests and the CLI use the short names.

Environment: `PIGGERY_DISABLED=1` makes an adapter inert (a session that must not join);
`PIGGERY_INSTALL_DIR` changes the local installer destination. `install.sh` builds this checkout;
it does not download a release or accept a release-version selector. Piggery uses `~/.piggery`;
the local installer rejects a nondefault `PIGGERY_HOME` because the daemon does not support it.

## config.yaml

`~/.piggery/config.yaml`. An unknown key or a bad value stops the daemon; the reason is in
`serve.log`. Changes need `piggery restart` unless noted.

| Key | Default | Meaning |
|---|---|---|
| `harness` | `pi` | Harness of a worker whose role and founding session name none: `pi`, `claude`, `codex`, `omp`, `dsh` |
| `gc.closed_after` | `14d` | Delete (after archiving) a team closed, or a solo session gone, longer than this: `14d`, `36h`, or `off` |
| `gc.archive_keep` | `30d` | Delete gc archives older than this, or `off`; also for a manual gc |
| `display.columns` | `[role, state, harness, model, ctx, turns, unacked, age, since, cwd]` | Columns `top` and `ps` show after the name, in order (`top` has no `unacked` column: its header, team lines and details give it); **live**, read on every run. An unknown name warns and shows the defaults; `-` where a row has no value, `model` is the id without its provider, `cwd` is blank in the project directory itself |
| `update.check` | `true` | Once a day the daemon asks GitHub `releases/latest` (the call `update --check` makes) and keeps the tag in `cache/update.json`; while a newer release exists `top`, `ps --view` (`daemon.update`), `setup` and `update --check` say `vX available: piggery update`. Nothing is installed. A build from source (`dev`, `dev-<sha>`) never asks, a failed call is silent and tried again the next day; `false` turns it off (the daemon reads it at its start) |
| `spawn.allowed_roots` | `[]` | Absolute directories outside a team's root where a worker may be placed with a `cwd` (the root and its repo's git worktrees always may) |
| `prompts` | `[]` | Your files by role: a list of `{file, roles}`; `file` is relative to `~/.piggery` or absolute; `roles` are `<role>`, `<template>/<role>`, `<template>/*` (every role of that template), `*` (every role of every template, and solo) or `solo`; a file several entries name is added once. The list needs a restart; a file is read at each session start. A bad entry is skipped with a line in `serve.log` |

## harness/<harness>.json

One file per harness in `~/.piggery/harness/`, written by `piggery setup`, read at each spawn or
resume. Missing keys are added with their defaults; `setup --force` writes the defaults again.

| Key | In | Default | Meaning |
|---|---|---|---|
| `cmd`, `args` | all | the harness's command (`pi --mode rpc -e <extension>`, `claude`, `codex`, `omp --mode rpc`, `dsh`) | What runs; dsh can be `npx` with `["-y", "@deepseek-ai/dsh@<version>"]` |
| `env` | claude, codex, dsh | `[]` | Extra `KEY=VALUE` for the worker |
| `model`, `thinking` | all | `inherit` | The worker's model and thinking level, in the harness's own names and levels, passed as written; `inherit` goes on down the chain (role `spawn.model`, this file, the founding session's model, the harness default) |
| `blacklist` | all | `[]` | What a worker must not load from your own setup: packages, extensions and MCP servers (pi, omp, claude, codex), or rows of your dsh setup. piggery's own copies and `pi-peer` are always left out |
| `disallowed_tools` | claude | Claude's own tools that reach you or start agents outside piggery | A role gets one back with `spawn.allow_tools` |
| `disabled_tools` | codex | `agents`, `apps`, `goals` (name to `-c` setting) | Same, by name |
| `disabled_tools` | dsh | dsh's subagent, messaging, plan and goal tools | Same, a list of tool names |
| `tested_versions` | all | the version piggery was tested with | `setup` and `doctor` warn about another one |

## Manifest

`~/.piggery/templates/<name>/manifest.yaml`. Frozen into a team when it is brought up: a change
applies to the next team only. A manifest with a key this list does not have, or a wrong value, is
refused with the reason when the team is brought up.

| Key | Default | Meaning |
|---|---|---|
| `template` | required | The template's name; also the default team name |
| `summary` | `""` | One line, when to use it (shown when an agent lists templates) |
| `auto_join_role` | `""` | The role the founding session takes; needed when there are several roles |
| `roles.<r>.description` | `""` | One line, what the role does |
| `roles.<r>.instructions` / `instructions_file` | none | The role's prompt, inline or a file next to the manifest |
| `roles.<r>.tools` | `[]` | Model tools the role has: `send` (also board pins and `watch`), `inbox`, `who`, `agent` |
| `roles.<r>.can_spawn` | `[]` | Roles this role may spawn |
| `roles.<r>.can_pin` | `false` | May pin to the team board |
| `roles.<r>.can_set_cwd` | `false` | May spawn a worker in another directory (see `spawn.allowed_roots`) |
| `roles.<r>.spawn.harness` | `inherit` | Harness of this role's workers; `inherit`: the founding session's, else `config.yaml` |
| `roles.<r>.spawn.model`, `.thinking` | `inherit` | This role's worker model and thinking level |
| `roles.<r>.spawn.allow_tools` | `[]` | Tools the harness profile turns off that this role keeps |
| `routing[]` | none = all denied | `{from, to, allow, cc}`; the first rule matching a sender and receiver decides; `cc` roles get a copy; a `to: notify` line is ignored with a warning (`piggery check` shows it), since only piggery writes to `notify` |
| `timers[]` | `[]` | `{on: <role>, silent_for: <duration>, notify: <role\|reports_to>}`: one notice when a working member has no turn end for that long; `notify: notify` is ignored |
| `limits.depth` | none | How deep spawn chains may go |
| `limits.concurrency` | none | Live workers at once (most built-ins set 10; `triple-review` sets 3) |
| `limits.messages_per_participant_per_minute` | none | Flood guard; a mail over it is held until you `release` it |
| `limits.max_respawn_per_hour` | none | A worker resumed that often in an hour is parked and its lead is told |

`none` means no limit. Roles that can spawn need `depth` and `concurrency`. There are no declared
tools beyond the four: give a role `send` and say in its prompt what to send.

## Harness capabilities

| | Worker (any harness) | pi, omp, dsh session | Claude Code session | Codex session |
|---|---|---|---|---|
| Wake when idle | yes | yes | yes | yes |
| Mail into a running turn | yes | yes (steered in) | after a tool call or at turn end | after a tool call or at turn end |
| `piggery abort` | yes | yes | no (only its own Esc) | no (only its own Esc) |
| `piggery model` | yes (Codex: from its next turn) | no: choose in the session | no | no |
| `kill`, `resume` | yes | no | no | no |
| Role card | system prompt (Codex: developer instructions) | system prompt | MCP instructions | MCP instructions |
| Context, turns and tail in `top` | yes | yes (its session file) | yes (its transcript) | yes (its rollout; none for an ephemeral thread) |

Mail is held while a session waits on a permission prompt (Claude, Codex, pi) and goes out after it;
omp does not report one. After Esc in Claude no signal reaches piggery, so the turn closes, unacked,
at your next prompt.

## What `setup` changes

`setup <harness>` changes that harness's own files, keeping the rest; run again it changes nothing
(after you move the binary it points them at the new place). `setup remove <harness>` takes out
exactly that. Copies piggery unpacks start with a `managed by piggery` line: do not edit them, and a directory
there that is not piggery's is left alone. Each installed thing has an integration version, raised
only when what is installed changes, so a new build alone never makes an install outdated. pi, omp
and dsh copies with a lower version are updated by the daemon at its start; claude, codex and paseo
are never changed on their own, only reported: `piggery setup --outdated` updates them. Local development uses `setup --refresh` to pick up changed bytes at the same integration version. `piggery setup` alone (and `doctor`) shows each
harness's version and problems, each with its fix, and `vN < vM` for an outdated install.

| Harness | Changed |
|---|---|
| pi | the extension unpacked into `~/.pi/agent/extensions/piggery/` (or under `$PI_CODING_AGENT_DIR`); `--ext PATH` points settings at a checkout instead |
| claude | `~/.piggery/claude/` (a local plugin marketplace with hooks); Claude's own commands `claude mcp add --scope user piggery`, `claude plugin marketplace add` and `claude plugin install piggery@piggery`; nothing in `~/.claude` is written by piggery |
| codex | piggery's hooks in `hooks.json`, after yours, and a block between `# >>> piggery` and `# <<< piggery` in `config.toml`, in `$CODEX_HOME` (default `~/.codex`) |
| omp | the extension unpacked into omp's agent dir (`~/.omp/agent/extensions/piggery/`; `$PI_CODING_AGENT_DIR` or a profile in `OMP_PROFILE`/`PI_PROFILE` moves it) |
| dsh | the plugin in `~/.piggery/plugins/dsh/`, and one block between `# BEGIN piggery` and `# END piggery` in dsh's home patch (`$DSH_HOME/cordis.patch.yml`, default `~/.dsh`); your rows stay |
| paseo | the plugin in `~/.piggery/paseo/`, installed with `paseo plugin install`; the Paseo daemon must run and every app showing it must be Paseo 0.9.1 or newer |

## Notify hooks

piggery tells your hooks what only it knows about a team's mail. Every regular executable file in
`~/.piggery/hooks/notify.d/` runs, in parallel, for each notice, with one JSON line on stdin: `id,
from_label, team, gate, dir, kind, body, created_at`. `gate` is the team's gate (for a solo, the
session itself), `dir` the team's root (a solo's directory), `body` one short sentence. A hook that
runs over 10 seconds is killed with its child processes; a failure is a line in `serve.log` with
the file's name and never the body. Without a hook nothing runs. `hooks/notify` (the old single
file) is not run; `piggery check` warns about it. `setup notify` writes and lists hooks.

| `kind` | A notice when |
|---|---|
| `reply` | the gate finished a turn on team mail and sent nothing: its answer is in its session |
| `settled` | the gate sent its last message and no member is working or has mail waiting |
| `failed` | the gate's turn on team mail failed: that mail waits for new mail to be given again |
| `gate_lost` | a team has no live member left (its mail and workers wait for the next gate) |

Only the engine writes to `notify`: an agent's send to it is refused, and a `to: notify` routing
line or `notify: notify` timer is ignored with a warning.

## Notes per harness

- **pi**: workers run with your pi setup minus the blacklist, plus piggery's extension, in a fresh agent
  dir per run; their sessions appear in your `/resume`; a project's `.pi/settings.json` still applies
  and can add back a blacklisted package. Turn off `pi-peer` while using piggery.
- **Claude Code**: workers run with your login, user settings, user MCP servers (minus the blacklist)
  and never a repo's project settings; your own hooks run in them too (make a hook that must not exit
  when `PIGGERY_ID` is set); their sessions are in `~/.claude/projects`. `/clear` keeps a session's
  participant. Restart Claude sessions that were open during setup.
- **Codex**: run `piggery setup codex` before using Codex workers or sessions: Codex runs a hook only
  once trusted. Workers use the Codex home of the daemon (`$CODEX_HOME`, else `$HOME/.codex`): a daemon
  started with another `HOME` has no login there and the first turn fails with 401. A worker's
  `request_user_input` goes unanswered and ends the turn.
- **omp**: mail that arrives as a run ends is steered in and shows in the TUI as a user message; Esc or
  `piggery abort` closes the turn unacked and the mail comes again. piggery does not see a permission
  approval, and `piggery model` cannot switch an omp session.
- **dsh**: restart a `dsh web` that was open during setup; a session's records are kept under
  `~/.piggery/sessions/dsh/`; a worker takes several seconds to start (dsh loads its plugins); model
  names are dsh's routes (`provider/model`); a custom provider is declared in your own dsh setup.

## Harness versions

Tested with pi 0.87.1, Claude Code 2.1.283, Codex 0.157.1, omp 18.4.2 and dsh 0.2.0-rc.1; `piggery
setup` and `doctor` warn about another version.
