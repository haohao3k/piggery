# Piggery guide

How to use piggery day to day, by task. Every key and command is listed in
[reference.md](reference.md); `piggery --help` lists the commands and `piggery <command> --help`
shows one. Install and first steps are in the [README](../README.md).

## Concepts in one screen

- **Session**: an agent you opened yourself (pi, Claude Code, Codex, omp or dsh). Once piggery is
  added to it (`piggery setup <harness>`), every new session starts **solo**: it is on the farm,
  in no team, with a mailbox and four tools (`send`, `inbox`, `who`, `agent`).
- **Team**: a group of agents that share a shape. A solo session founds one when you ask ("make a
  supervisor-executor team for this"); it becomes the team's **gate**, the one that talks to you
  and to other teams. Teams are named after their directory.
- **Role**: what one member of a team may do. The team's **template** says who may write to whom,
  who may spawn workers, which tools each role has, and how many workers may run at once. The
  farm checks this itself on every mail and spawn.
- **Worker**: a headless agent piggery starts for a role (`spawn`), on the harness the role's
  template names, else the harness of the session that founded the team. Workers you did not open
  can be watched, aborted, stopped and resumed by name.
- **Mail**: how agents talk. A message stays unread until the agent's turn that showed it ends
  normally, so a crash or an abort never loses it: it comes again.

## Run a team

**When**: work that splits into tasks, or needs a second opinion.

1. Add piggery once per harness: `piggery setup pi` (or `claude`, `codex`, `omp`, `dsh`), then
   restart sessions that were open. `piggery setup` alone shows where each harness stands.
2. Open the harness in your project and ask: *"found a supervisor-executor team to fix the failing
   tests"*. The session picks the template (`agent action=templates` lists them), founds the team
   rooted at its directory and becomes the gate. From a shell instead:
   `piggery team up supervisor-executor --cwd .`.
3. The gate spawns workers as the template allows, gives them tasks by mail, and gets a handback
   from each. You keep talking to the gate, in your own session.

Built-in templates: `supervisor-executor` (a supervisor splits the goal; executors do the tasks),
`slp` (a supervisor steers a lane: one lead, peers with separate scopes), `council` (a chair asks
members for independent views on one decision), `p2p` (peers that talk freely and spawn peers).

**Mixing harnesses.** A role can name a harness (`spawn: {harness: claude}` in the template); a
role that names none uses the harness of the session that founded the team, and a founder with none
uses `harness:` in `config.yaml`. So a pi gate can have Claude workers and a Codex reviewer. Each
harness needs its `piggery setup <harness>` and, for workers, its profile (`piggery setup` writes
them in `~/.piggery/harness/`).

**Join a team you already have open.** A solo session opened in a team's root directory can be
admitted by a member whose role may spawn that role: ask the member, "admit the session in
<name> as executor". Founding, being admitted, or resuming a session that was in a team still open are the only ways into a team for an open session.

**Limits.** The template caps how many workers run at once and how fast mail may flow; a mail over
the cap is held until you `release` it. A template is frozen into a team when it is brought up:
editing it changes the next team only.

## Watch and step in

```sh
piggery ps                    # teams, members and solo sessions by project, once
piggery top                   # the same, live, with context, turns and latest events (enter folds a team; click a worker's model to change it)
piggery tail w1 -n 50 -f      # a worker's log (or a session's transcript), readable; -f follows
piggery abort w1              # cancel its current turn (like Esc); it stays alive
piggery model w1 HP/kimi-k3   # its model from the next turn; --thinking high for the level
piggery kill w1               # stop its process now (short: piggery x w1; in top: x, then y)
piggery resume w1             # start a stopped worker again in its session
piggery team down demo        # close a team: workers stopped, nothing is acked
```

The target is a name or an id (`--team T` when a name is in several teams). `ps` and `top` only
read, except `x` in `top`. In `top`, a member's current task sits under its name: the latest mail marked as an
assignment from whoever it reports to, and whether it handed back.
Gone members with nobody live below them fold into one `✗ N gone` line per team (`ps` too); `enter`
on a team or on that line folds or opens it, and `top` remembers it.

**Emergency stop.** `x` in `top` on the selected worker (it asks `kill <name>? y/n` once), or
`piggery x <worker>`, kills it. Kill asks first: SIGTERM to the worker, so its harness and extensions
clean up (pi stops its shell commands, a background-jobs tool stops its jobs); after up to 2 seconds
it is SIGKILL for the worker and every process still left in its tree, shell commands in a process
group of their own included. `piggery resume` brings it back in its session and its unacked mail is
delivered again, so send it a note first if it must not redo the work. Something already detached
before `x` that no tool tracks (a bare `nohup … &`), and what was started outside the tree (a
container the docker daemon runs), may survive. A session you opened is not a headless worker: `x`
says so and does nothing; stop it in its own window (Esc).

What works on which session (details in [reference.md](reference.md#harness-capabilities)):
workers take every command above; a session you opened can be aborted on pi, omp and dsh, and its
model is changed in the session itself. Mail is held while a session waits on a permission prompt
and goes out after it.

**A mail was not delivered?** `piggery why a b` shows every gate check for a send from `a` to `b`
and sends nothing. A message held by a limit is delivered with `piggery release <id>`. `piggery
log` lists decisions and lifecycle events; `piggery doctor` lists what looks stuck (open turns,
unacked mail of agents that are gone, workers without a process) and exits 1 when it finds any.

**Notifications.** piggery tells you what only it knows: when the mail flow of a team needs you.
`piggery setup notify add desktop`, `add herdr` or `add ntfy:<topic>` writes a hook for you into
`~/.piggery/hooks/notify.d/` (it needs `jq`; `piggery setup notify` lists what is there and what each
target lacks; `remove <target>` takes one out; it works from the next notice, no restart). Or write
your own: each executable file in `~/.piggery/hooks/notify.d/` is run, all of them in parallel, with
one JSON line on stdin: `id, kind, team, gate, dir, body, created_at`. `gate` is your team's gate (your session; for a solo, itself),
`dir` the team's root (a solo's directory) and `body` one short sentence. `kind` is one of:

| `kind` | When |
|---|---|
| `reply` | the gate finished a turn on team mail and sent nothing: its answer is in its session |
| `settled` | the gate sent its last message and no member is working or has mail waiting |
| `failed` | the gate's turn on team mail failed: that mail waits for new mail to be given again |
| `gate_lost` | a team has no live member left (its mail and workers wait for the next gate) |

It never fires for a chat turn of yours, a turn that is not over mail, an interrupted turn, a
headless worker, or a member that is not the gate; whether you are looking at the session is for
the hook to decide. Agents cannot send to `notify`. A hook passes `body` to any command as an
argument, never inside a command string: it carries names that users and agents chose. A hook that
runs longer than 10 seconds is killed; an error is a line in `serve.log` with the file's name. The old
single `hooks/notify` is not run any more: move it into `hooks/notify.d/` (`piggery check` says so).
`top` shows the latest notices in a box beside its events; `n` opens it.

## Between teams

Teams are isolated, whatever their directories. The only channel between two teams, or between a
team and a solo session, is **gate to gate**: writing to a team's name reaches its gate, a solo
session is its own gate, and a member that is not its team's gate asks its gate to relay. `who`
(what the agents call for) lists your team and then one line per other team and solo session.
Mail shows where it came from, for example `from="bme (peer, team B)"`.

A session can leave a team by founding a new one: its workers and unread mail move to the team's
next gate.

## Close, reopen, clean up

- **Close**: ask the gate to close its team, or `piggery team down demo`. Workers stop, every
  member is gone, unread mail stays unread, and a session that was in the team becomes solo in the
  same session, so you can keep chatting or found a new team.
- **Reopen**: until cleanup removes it, ask a solo session in the team's root, "reopen team demo".
  The team comes back with the manifest it was brought up with; workers stay stopped until the gate
  resumes them.
- **Clean up** is automatic. The daemon runs it at start and every 24 hours: a team closed for
  longer than `gc.closed_after` (14 days) and a solo session gone for that long are archived to
  `~/.piggery/archive/`, read back, and deleted with their logs and scratch files; archives older
  than `gc.archive_keep` (30 days) are deleted. By hand: `piggery gc --closed-before 168h
  --dry-run`, then without `--dry-run`; `piggery archive show <file>` reads an archive. Your
  harnesses' own session files are never touched.

## Customize piggery (`~/.piggery`)

Everything piggery keeps is in `~/.piggery`. Some of it is yours to edit; the rest is piggery's.

| Path | Yours to edit? | What it is |
|---|---|---|
| `config.yaml` | yes | daemon settings; every key with its default and a comment |
| `templates/<name>/` | yes | team templates: `manifest.yaml` and the prompt files it names |
| `harness/<harness>.json` | yes | how workers of one harness start (command, model, blacklist) |
| `hooks/notify.d/` | yes | your notification hooks (above) |
| `rules/*.md`, any file you name | yes | your own rules for `prompts:` (below) |
| `serve.log` | read | the daemon's log: **where problems are written** |
| `piggery.db`, `piggery.sock`, `piggery.lock`, `admin.token` | no | state, socket, lock, your admin credential |
| `logs/`, `run/`, `sessions/`, `archive/`, `backups/`, `cache/`, `plugins/` | no | worker logs, scratch of running workers, session data, gc archives, database copies, cached reads, copies of piggery's adapters |
| `claude/`, `paseo/` | no | what `setup claude` and `setup paseo` install |

`piggery setup` writes missing files and adds the keys a file lacks, keeping your values, comments
and order. It never overwrites what you wrote.

**When a change takes effect.** `config.yaml`: after a restart (`piggery restart`; it stops workers,
`piggery resume <name>` brings them back), except `display.columns`, read on every run.
`harness/<harness>.json`: at the next spawn or resume. A template: at the next `team up` (a
running team keeps the one it started with). A rules file: at the next session start.

Run `piggery check` before `piggery restart`: it reads `config.yaml`, the harness profiles, your
templates and the `prompts` entries the way the daemon will, and prints what would be refused (exit 1)
and what would be skipped or ignored (`warning:`), without starting or changing anything.

**On a mistake.** A key `config.yaml` does not know, or a bad value, stops the daemon from
starting: the command only says it could not connect, and the reason, naming the key, is in
`~/.piggery/serve.log`. A mistake in `prompts:` never stops anything: the bad entry is skipped and
`serve.log` has one line naming it. A template that does not parse is refused when you bring the
team up, with the reason.

### Recipes

**Default harness for workers** (used when neither the role nor the founding session names one):

```yaml
# config.yaml
harness: claude        # pi, claude, codex, omp or dsh
```

**Model and thinking for a harness's workers, or for one role.** A worker runs the first of: the
role's `spawn.model` in the template, the harness profile's `model`, the model of the session that
began the spawn chain, the harness default. `inherit` (the default) means "keep going down the
chain". Thinking follows the same chain, in the harness's own levels. Pin the harness when you pin
a model: names and levels belong to one harness (`team up` warns when a role sets `spawn.model` or
`spawn.thinking` and leaves `spawn.harness` as `inherit`).

```jsonc
// harness/claude.json: every Claude worker
{ "model": "sonnet", "thinking": "high" }
```

```yaml
# templates/mine/manifest.yaml: workers of this role only
roles:
  reviewer:
    spawn: {harness: codex, model: gpt-5, thinking: high}
```

Change a running worker with `piggery model w1 <model>`. A level the harness does not run for that
model is an error at spawn, never a silent downgrade.

**Keep things out of workers.** A worker runs with your own harness setup (extensions, skills, MCP
servers) minus the `blacklist` of its profile: names of packages, extensions or MCP servers to leave
out. A profile can also switch off a harness's own tools that would reach you or start agents
outside piggery (`disallowed_tools` for Claude, `disabled_tools` for Codex and dsh); a role gets one
back with `spawn: {allow_tools: [...]}`.

**Rules that follow you** (how you want code written, how to split work), added to the cards of the
roles you name, in every project and harness:

```yaml
# config.yaml
prompts:
  - file: rules/code.md                 # relative to ~/.piggery, or absolute
    roles: [executor, peer, lead]
  - file: rules/delegation.md
    roles: [supervisor-executor/supervisor, chair, solo]
```

A role is `<role>` (in every template), `<template>/<role>` (that template only; the template is the
`template:` line of its manifest) or `solo`; `<template>/*` is every role of that template and `*` is
every role of every template, solo included (a file several entries name is added once). The text goes right after the role's own instructions,
under a heading naming the file. Files are read when a session starts, so edits need no restart, but
a session already running keeps its card. The list itself needs a restart.

**Your own template.**

```sh
piggery template new mine --from supervisor-executor
$EDITOR ~/.piggery/templates/mine/manifest.yaml
piggery team up mine --cwd .
```

Then change the roles, their prompts (`instructions:` inline or `instructions_file:` next to the
manifest), the routing rules (the first rule matching a sender and receiver decides; none means
denied), the limits and the timers:

```yaml
template: mine                 # the template's name; also the default team name
roles:
  supervisor: {tools: [send, inbox, who, agent], can_spawn: [executor], instructions_file: prompts/supervisor.md}
  executor:   {tools: [send, inbox, who], spawn: {model: inherit}}
routing:
  - {from: supervisor, to: executor, allow: true}
  - {from: executor, to: supervisor, allow: true, cc: [supervisor]}
limits: {depth: 2, concurrency: 4, messages_per_participant_per_minute: 30}
timers:
  - {on: executor, silent_for: 20m, notify: reports_to}   # nudge when a worker is silent that long
```

`piggery` keeps a built-in you never edited up to date; a file you edited or deleted is left as it
is, and your own templates are never touched. `piggery template new` copies the current built-in
under a new name if you want the newer version of one you edited. Every key of a manifest is in
[reference.md](reference.md#manifest).

**Where workers run.** A worker starts in its spawner's directory. To put workers in lanes (a git
worktree per lane, made by the agent), give the role `can_set_cwd: true`; the directory must be
inside the team's root, in a git worktree of the repo at the root, or inside a directory you list:

```yaml
# config.yaml
spawn:
  allowed_roots: [/home/me/worktrees]   # absolute paths; the default is none
```

**`top` and `ps` columns.** Live, no restart: `display.columns: [role, state, model, ctx, turns]`.
Remove a column to hide it; the name is always shown.

**How long history stays.** `gc.closed_after: 30d` (or `36h`; `off` keeps closed teams forever) and
`gc.archive_keep: off`.

## Troubleshooting

- **The daemon does not start, or a command says it cannot connect.** Read the end of
  `~/.piggery/serve.log`: a bad `config.yaml` key or value is named there. `piggery` alone says
  whether the daemon runs and never starts it. You never start it by hand: the next command does, so `piggery` must be on the PATH your harness sees.
- **Where each harness stands.** `piggery setup` (no argument) shows, for every harness, its version,
  whether piggery is installed in it and runs this binary, and a fix command at the end of each
  problem line. `piggery doctor` prints the same problems as warnings. Run `piggery setup <harness>`
  again after you move the `piggery` binary, and restart sessions that were open during setup.
- **Outdated integrations.** After an upgrade the daemon brings the pi, omp and dsh extensions up to date
  itself. Claude Code, Codex and Paseo are only reported: `ps` and `top` show `outdated: codex (v0 < v1):
  piggery setup --outdated`. Run that command: it updates every installed integration that is outdated
  and says what to do next (restart the sessions that were open, reload the Paseo app).
- **A worker does not start.** Its harness profile is missing or wrong (`piggery setup` writes them),
  or the harness has no login for the daemon's `HOME`. `piggery tail <worker>` shows the harness's
  own error.
- **Upgrade.** `piggery update` installs the latest release in place of the running binary (its
  checksum is verified; `--check` only prints versions; a build from source needs `--force`), or run
  the install script again. The daemon restarts at the next command; before a database upgrade it
  copies the database to `~/.piggery/backups/` (the three newest are kept). `piggery restart`
  restarts it now.
  Once a day the daemon checks for a newer release: `top`, `ps --view` and `setup` then say `vX
  available: piggery update`, and nothing is installed on its own. `update.check: false` in
  `config.yaml` turns the check off; a build from source never asks.
- **After a crash or a restart.** State and mail are in SQLite, so nothing is lost. The next command
  starts the daemon again and open sessions reconnect by themselves. It marks every agent whose
  process or connection is gone as `gone`, never respawns workers and never acks mail: bring a worker
  back with `piggery resume <name>`, and its unread mail comes again on its next run.
- **Turn piggery off for one session**, for example to read an old session's history without
  joining: `PIGGERY_DISABLED=1 pi --resume …` (the pi, omp and dsh adapters honor it).
- **Two mail systems.** Remove `pi-peer` from pi's packages while using piggery: both give the model a
  `send`-style mailbox.
