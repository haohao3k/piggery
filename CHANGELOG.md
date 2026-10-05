# Changelog

## v0.7.1 - 2026-10-05

Codex threads that share one app-server are no longer mixed up.

After upgrading, run `piggery restart`, then restart Codex (the Desktop app, or the shared
`codex app-server` and its TUIs). No integration changed, so `setup --outdated` has nothing to do.

- Fix: Codex Desktop, and TUIs started with `--remote`, run every thread in one `codex app-server`,
  and piggery made all of them one participant, so mail or a wake could reach the wrong thread or
  project. Each thread is now its own participant; a piggery tool call there that does not say
  which thread it is from is refused. A Codex TUI and Claude still keep one participant across
  `/clear`. Participants already mixed up stay so until they are closed. Reported by @haohao3k
  (#4).
- Fix: `--admin` on an agent's command (such as `who`) now points to `piggery ps` instead of the
  removed `dump`, and `gc`'s usage says `--closed-before DURATION` (such as `168h`).

## v0.7.0 - 2026-10-03

piggery now tells you, by itself, when a team's mail flow needs you, and `piggery check` tests your
files before a restart.

After upgrading, run `piggery setup --outdated`: the pi and omp integrations are now 3 and dsh 4
(Paseo, Claude and Codex are unchanged). Then restart the pi, omp and dsh sessions that were open.

Breaking: only piggery writes to `notify` now. An agent's send to `notify` is refused, and a
`to: notify` routing line or a `notify: notify` timer in a template is ignored with a warning
(`piggery check` lists them). Notify hooks run from `~/.piggery/hooks/notify.d/`; a single
`~/.piggery/hooks/notify` is no longer run, so move it into that directory.

- Notices, decided by the engine at the end of a gate's turn, from the team's mail alone: `reply`
  (a turn on team mail ended and the gate sent nothing), `settled` (the gate sent its last message
  and nobody works or has mail waiting), `failed` (a turn on team mail failed, so that mail waits)
  and `gate_lost`. Your chat with the gate, a gate that is still dispatching work, a headless gate
  and a member that is not the gate never notify. The hook's JSON line adds `gate` and `dir`,
  and `kind` is the notice's kind. The respawn-limit notice goes to the worker's lead only.
- `piggery setup notify add desktop|herdr|ntfy:<topic>` writes a ready hook into `notify.d/`
  (`remove` takes it out; alone, it lists the hooks and what each needs). Every file there runs in
  parallel, each with its own 10-second limit.
- `piggery check`: reads `config.yaml`, the harness profiles, every template and your prompts with
  the daemon's own loaders and prints what they would refuse or ignore; it changes nothing.
- The daemon checks once a day for a newer release; `top`, `setup` and `update --check` say `vX
  available: piggery update`. Nothing is installed on its own; `update.check: false` turns it off.
- `top` shows the latest notices at the bottom left, beside the events; `n` opens or closes them,
  like `e` for the events. `ps --view` carries them as `notices`, and the update notice as
  `daemon.update`.
- The template list an agent reads now also shows a template that `team up` would refuse.
- Fix: a build stamped `dev-<sha>` is treated as a build from source: `piggery update` no longer
  replaces it with an older release unless `--force`.

## v0.6.0 - 2026-10-01

The Paseo plugin is rebuilt on what `piggery top` shows, and a project no longer jumps around the
list when its sessions reconnect.

After upgrading, run `piggery setup --outdated`: the pi and omp integrations are now 2, dsh 3 and
Paseo 3. Then restart the pi, omp and dsh sessions that were open, and reload the Paseo app.

- Paseo plugin, rebuilt: the Overview has `top`'s rows, order, state words, folds and since, one
  line per worker with a ctx column, a header with working/idle/waiting counts, events as short
  lines, and a dialog with a worker's details and tail. A new Board tab gives one band per project
  and a column per status (working, idle, waiting, gone), filtered by team; projects with nothing
  live start folded, and gone workers show on request.
- `piggery ps --view` and `piggery tail --view` print what `top` shows as versioned JSON (rows with
  their actions, header counts, events, Overview; tail lines with their kind). The Paseo plugin reads
  them from the installed binary, so `top`, `ps` and the plugin always agree.
- Projects are listed live first, then sleeping (nothing working and no real turn for over a day),
  all gone, closed. A live project sorts by its latest real turn; a reconnect or a daemon restart
  moves nothing.
- An idle worker's or solo's since counts from its last real turn, not from a reconnect. `ps --json`
  gives a solo's `last_turn_end` too.
- `piggery top`: in a live headless worker's Overview, a click on the model (blue, `▾`) or `M` opens a
  picker of the models its harness offers, with the thinking level; `enter` or a double-click
  applies (a dsh worker lists its models once the dsh integration is updated). The footer always
  lists `M model`, dim where it does nothing.
- Fix: in pi, omp and dsh sessions, a wake or reconnect that finds no mail is not a turn any more (no
  empty turn, last turn kept); needs the updated integration.
- Fix: in `top`'s Overview, the last turn no longer runs into the joined/spawned value.

## v0.5.3 - 2026-09-30

Big teams stay readable in `piggery top`, and a worker's piggery tool call survives a daemon restart.

After upgrading, reload the Paseo app if you use its plugin (its integration is now 2; `piggery setup
--outdated` updates it).

- `piggery top`: Enter opens or closes everywhere: a team (live ones too), the gone line, a member's
  details; `t` only switches Overview/Tail. A collapsed team is one line with its working/idle/gone
  counts and held mail, and top remembers which teams you opened or closed (`<dir>/cache/top.json`).
- Gone members with no live worker under them fold into one dim row at the bottom of their team
  (`▸ N members  ✗ gone  <since>`, on the columns), in `top`, `piggery ps` and the Paseo plugin;
  Enter expands it. A team's members are indented under the team's title, and the list no longer
  has an unacked column (the header, details and a folded team still show it).
- `piggery top` and the Paseo plugin: all-gone and closed teams are rows on the columns
  (`▸ team old  ✗ gone  15h`), a solo row has a team row's shape, and the state column keeps one
  width whatever the states, so the layout depends only on the window.
- `piggery top` and the Paseo plugin: the gate is tagged on its member (`summer-hamster (gate)`)
  instead of on the team line; a requested worker shows as `◌ queued` (ps keeps `requested`); the
  cwd column shows only when someone works outside their group's directory.
- `piggery top` scrolls a long list under a column header that stays put, with `↑ N` / `↓ N` on the
  border for rows out of view; PgUp/PgDn and Home/End (`? all keys`). Opening, closing or scrolling
  never moves a column, and a collapsed team line drops whole parts to fit instead of being cut.
- `piggery top`: the footer says whether the mouse is captured (`m mouse on` / `m mouse off`), and
  events show their time (`14:12:05` today, `09-29 14:12` before) instead of how long ago. Events
  start folded; `e` opens them and top remembers it.
- Paseo plugin, like top: Events fold (folded by default), event times, a folded team shows its
  counts, and folds are remembered on the Paseo host.
- `prompts` in config.yaml takes `*` (every role, every template, and solo sessions) and
  `<template>/*` (every role of that template); a file several entries name is added once.
- `team up` warns when a role pins `spawn.model` or `spawn.thinking` but leaves `harness: inherit`:
  a model name belongs to one harness.
- Fix: a Claude Code or Codex worker's piggery tool call made while the daemon restarts waits up to
  10 seconds for the connection and then runs, instead of failing. A call already sent when the
  connection dropped still fails, so nothing runs twice.
- Fix: a tool call right after the first connect could fail with "piggery is not reachable right
  now".
- Fix: `piggery x <name>` (and abort, model, resume, tail) no longer refuses a name when the other
  matches are gone members of closed teams; the live participant wins.

## v0.5.2 - 2026-09-30

Fix: `x` and `kill` now stop the command a worker is running, not only the worker.

- `x` / `kill` left a worker's running shell command alive when the harness ran it in a process group of
  its own (pi's bash tool does). Kill now asks first (SIGTERM, so the harness and its extensions clean
  up), waits up to 2 seconds, then SIGKILLs the worker and everything left in its process tree; stop
  ends the tree the same way. Something detached before the kill that no tool tracks may survive.

## v0.5.1 - 2026-09-30

An emergency stop in `piggery top` (`x` kills the selected worker) and a tidier bottom of the screen.

- Emergency stop: `x` in `piggery top` kills the selected headless worker (it asks `kill <name>? y/n`
  once; a session you opened gets a reason and nothing else), and `piggery x <worker>` is the short
  form of `piggery kill`.
- `piggery top`: events in a box that `e` folds to one line (`● Events · <latest>`; it starts folded
  under 30 rows), and the keys on two aligned lines under a rule.

## v0.5.0 - 2026-09-30

`piggery setup --outdated` brings what piggery installed for your harnesses up to date, and user docs
are public: `docs/guide.md` and `docs/reference.md`.

After upgrading, run `piggery setup --outdated`; reload the Paseo app if you use its plugin. Each
thing piggery installs for a harness now has an integration version, and whether it is outdated is
decided by that number, not by the build.

- One integer for each of pi, omp, dsh, claude, codex and paseo, bumped only when what is installed
  changes; an install carries it as `PIGGERY_INTEGRATION_VERSION=N`. A rebuild that leaves the
  installed part as it was no longer reads as outdated or rewrites anything. Installs from before
  this read as outdated once. pi, omp and dsh are brought up by the daemon at its start; for claude,
  codex and paseo the daemon logs one warning, `ps` and `top` show `outdated: claude (v1 < v2):
  piggery setup --outdated` (`ps --json` has the list as `outdated`, which the Paseo plugin reads
  instead of running a command), and `setup` and `doctor` show `vN < vM` (a Codex hook missing from
  `hooks.json` counts).
- `piggery setup --outdated` updates every installed integration that is outdated (`setup <harness>`
  for each, one line per update and what to do after); not installed ones are untouched, and it says
  so when all is current. `install.sh` no longer runs anything after an install: it prints
  `piggery setup <harness>` for a new machine and `piggery setup --outdated` for an upgrade.
- Docs: `docs/guide.md` (by task, including how to customize `~/.piggery`) and `docs/reference.md`
  (every command, every key of `config.yaml`, the harness profiles and the template manifest, and
  what each harness can do), linked from the README. The adapters' "piggery binary is not on PATH"
  error links the guide.
- `top` shows the daemon's version at the end of its key footer; when the `piggery` you run is
  another build, it is amber with `(cli <version>: piggery restart)`. `ps --json` has `version`.
- Paseo plugin: a session you opened has a Tail and ctx/turns (from its transcript), a member's
  Overview shows its current task (handed back, newer mail), and the list starts with top's status
  line: daemon age, teams, working and idle, held, unacked, version, and the outdated notice.

## v0.4.0 - 2026-09-29

Shared prompts: your own rules (code style, how you organise a project) go into the role card of
the roles you pick, in every template and harness.

- `config.yaml` `prompts: [{file, roles}]`: a role is `<role>` (every template), `<template>/<role>`
  or `solo`. The file is read each time a card is built, so an edit reaches the next session with no
  restart. A mistake (an unreadable file, an unknown template, a role in no template) is a warning
  in `~/.piggery/serve.log` and that part is skipped; it never stops the daemon or a spawn.
- A template's name key is `template:` (was `model:`, easy to read as the AI model). Your templates
  are rewritten in place at `setup` or daemon start (only that key); teams already made keep
  working, and a file with `model:` is still read.
- Mail headers name the sender's real role and the relation: `ana (supervisor, you report to
  them)`, `bo (executor, reports to you)`; before, any superior read "your lead" and a same-role
  member "your peer". Codex workers are told to ask the member they report to, not "your lead".

## v0.3.0 - 2026-09-29

Two new harnesses, omp (oh-my-pi) and dsh (DeepSeek Harness 0.2), and each member's current task
in `piggery top`.

Breaking: mail threads, `expects_reply` and the limits `max_hops` and `messages_per_thread` are
removed, and a team template that sets either limit to a number is refused; delete the key. Nothing
in piggery acted on them: `reply_to` stays, and `messages_per_participant_per_minute` is the flood
guard. Mail held by a removed limit is released when the daemon upgrades its database. Teams
already up keep working. Run `piggery setup pi` again so pi sessions stop showing `thread=`.

- omp: `piggery setup omp` adds piggery to omp sessions (mail is steered in and shows in the
  session); a role can run omp workers (`harness: omp`), resumed with their context after a stop.
- dsh 0.2.0-rc.1: `piggery setup dsh` adds piggery to `dsh web` sessions; a role can run dsh
  workers (`harness: dsh`). Workers turn off dsh's upload of session logs to DeepSeek.
- `send --op assign` marks a mail as the member's current task (a spawn or resume task is one);
  `top`'s Overview shows it, whether it was handed back, and a newer unmarked mail.
- `reply_to` takes a bare `N` as well as `#N`.
- The daemon backs up its database before a schema upgrade (`~/.piggery/backups/`, the 3 newest
  kept).
- `~/.piggery` is laid out as `plugins/`, `run/` (per-run scratch), `sessions/` (session data
  piggery keeps); gc removes a removed participant's entries there and in `logs/`, and now also
  removes a solo session gone longer than `gc.closed_after` (archived first, like a closed team).
- `send` answers `sent #N`; a mail's header has no `thread=`; `send --expects-reply` is gone.
- `top`: an open team whose members are all gone is one line; a model switched by `piggery model`
  shows at once. The Paseo plugin starts such a team collapsed (every Paseo app needs 0.9.1+).
- The built-in templates allow 10 live workers at once (`limits.concurrency`, was 4 or 5).
- An installed built-in template file you edited to exactly the new built-in is updated again by
  later versions.
- The gate roles' prompts and the solo card point to `piggery skills` for the rest of piggery.
- One-line install for Linux and macOS:
  `curl -fsSL https://raw.githubusercontent.com/sting8k/piggery/main/install.sh | sh` picks the
  build for your OS and CPU, checks it against the release's `checksums.txt`, and installs it in
  `~/.local/bin` (`PIGGERY_INSTALL_DIR`, `PIGGERY_VERSION` to change).
- A release's notes on GitHub are its section of this changelog.
- Tests that wait for the daemon to start allow 10 seconds, so a slow CI runner no longer fails them.

## v0.2.0 - 2026-09-29

Breaking: a team template with a `tools:` section is now refused. Give its roles `send` and say in
their prompt what to send to whom, then delete the section. An installed built-in template you
edited is not updated for you; edit it the same way.

- Every role of the built-in templates talks with `send` (no `done`/`ask`/`answer` tools).
- Declarative tools are removed: a template with a `tools:` section is refused; use `send`.
- A new session's default name is one word.
- Interactive sessions (pi, Claude, Codex; solos too) show ctx, turns and a tail in `top`, `ps --json` and `piggery tail`, read from the harness's own transcript.

## v0.1.0 - 2026-09-28

First release.

- One Go binary with a local daemon (SQLite, unix socket). Nothing runs in the cloud.
- Mail that waits for an agent to be back, and counts as delivered only when the agent's turn
  that read it has finished.
- Team layouts as YAML: roles, who may talk to whom, who may spawn whom. The daemon checks every
  send and spawn. Built-in: `supervisor-executor`, `slp`, `council`, `p2p`; make your own with
  `piggery template new`.
- Harnesses: pi, Claude Code and Codex (`piggery setup`). Headless workers run in their own
  sessions and can be stopped, resumed and switched to another model.
- Watch and step in: `top`, `ps`, `log`, `tail`, `why`, `abort`, `kill`, `resume`, `model`,
  `release`.
- Paseo plugin: `piggery setup paseo` adds a Piggery view (the same as `piggery top`) to Paseo.
- Upkeep: `gc` archives closed teams, `doctor` checks the daemon's state, `update` installs a
  newer release.
