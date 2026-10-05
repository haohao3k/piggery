# opencode capture capture (a): opencode 1.18.34 plugin surface for TUI sessions

Evidence only, no piggery code. opencode 1.18.34 (`/tmp/oc/pkg`), source `/tmp/opencode-src` at 907b3bc, model
`hp/glm-5.3-flash` only (about 20 model turns, plus one title-generator call per new session). Everything ran under
`/tmp/oc` (a copy of `/tmp/oc/xdg`, XDG dirs there); `~/.config|.local/share|.cache|.local/state/opencode` were
checked before and after (every file's mtime and size, `find ... | stat`): unchanged. The key (`HP_KEY`) is read from
the environment by the provider config (`{env:HP_KEY}`); `probe.js` redacts it, and a search of every file here for its
value finds nothing. All processes started (tmux server, TUIs, `opencode serve` on 4101-4105) were stopped by pid;
none left (`pids` list checked with `kill -0`, no listener on 41xx).

**File:line** below means: the capture file, then the line number = the `n` field of the probe line (one JSONL line per
hook call or event; times are ms since epoch in `t`). `captures/timelines.txt` has a compact view of every scenario
(`99-timelines.sh`), with the line ranges.

## Files

| Path | What |
|---|---|
| `probe/probe.js` | The probe plugin (`export default {id, server}`): logs every hook and event as JSONL (`PROBE_LOG`), tags each line with `inst` (one number per plugin instance), runs commands from a file (`PROBE_CTL`: `{"id", "call": "session.promptAsync", "args": {...}}` is run on the plugin's own `client`), and has two tools, `probe_whoami` (returns its context ids) and `probe_sleep` (waits N seconds, logs when `ctx.abort` fires). `flags.json` makes it add a system string (`system_add`) or answer `permission.ask` (`perm`). Never writes the key: strings through `redact()`, keys named like a secret dropped, long strings clipped (`…[+N chars]`) |
| `probe/opencode.json` | The private user config used (provider `hp`, key as `{env:HP_KEY}`; model `glm-5.3-flash` with `low`/`high` variants and an alias `glm-b` of the same upstream model, to have a second model to switch to) |
| `lib.sh` | Helpers: env, `serve_start`, a private tmux server (`tmux -L oca`, 200x50) that runs the TUI, `ctl` (append a command line), `wait_idle` |
| `01-loading.sh` | Q1: five ways to load the probe, one `opencode serve` each, no model call. Output `captures/01-{A..E}-*` (`*.probe.jsonl` = the probe log, `*.files.txt` = what was in the config dir afterwards, `serve-410x.{out,err}`) |
| `10-persist.sh` | Q10: read-only SQL and CLI reads of `opencode.db` while the TUI was still open. Output `captures/10-*.txt` |
| `timeline.sh`, `99-timelines.sh` | Compact event timelines → `captures/timelines.txt` |
| `captures/tui-1-port-turns-steer.jsonl` | TUI (`opencode --port 4110`, in tmux): plain turn (1-168), tool-call turn (172-281), prompt typed while busy (285-388). Line 392 is a failed first control command (my bug: lost `this`; fixed in the next file) |
| `captures/tui-2-port-wake-abort.jsonl` | Same TUI restarted with `--continue`: wake by `promptAsync` (60-166), `noReply` (170-175), Esc Esc abort with a pending steer (176-251), `POST /session/:id/abort` busy and idle (252-342), the next prompt (343-420) |
| `captures/tui-3-port-permission-model-sessions.jsonl` | `OPENCODE_CONFIG_CONTENT={"permission":{"edit":"ask"}}`: permission (60-198), model+variant change (203-248), `/new` (252-326), served second session (330-383), task-tool child (387-556), `session.get/children/list/status` (560-563), another directory (564-569), steer through the plugin client (570-695; ran twice, see Q5) |
| `captures/tui-4-noport-config-content.jsonl` | TUI **without** `--port`, plugin loaded from `OPENCODE_CONFIG_CONTENT` (the plugins dir removed): load (1), steer through the plugin client (55-216), wake (220-281), noReply (285-290). `screens/tui-4-*.txt` are the TUI screens (tmux `capture-pane`) before and after the wake and after noReply |
| `captures/tui-0-port-first-turn-no-inst-tag.jsonl` | First TUI run, before `inst` tags existed: the first turn, kept because it is where the two-loads finding was first seen |
| `captures/01-F-tui-noport.probe.jsonl` | TUI without `--port`, started and closed, no model call (load, client, dispose) |
| `captures/10-*.txt` | Q10 output (see there) |

Commands: `01-loading.sh` and `10-persist.sh` run as they are (they need `/tmp/oc` as described above). The TUI runs
were driven by hand with `lib.sh` (`tui_start LOG --port 4110 [--continue]`, `tkeys`, `tcap`, `ctl`); the exact prompts are
in the `chat.message` lines of each capture (`output_before.parts[0].text`).

## Answers

### 1. Loading

- **All four ways load it**, each with one `plugin()` call and `config` hook: a file in `$XDG_CONFIG_HOME/opencode/plugins/`
  (`01-A-plugins-dir.probe.jsonl:1`), config `plugin: [[spec, options]]` (`01-B...:1`, options `{"opt":1}` arrive as the second
  argument), `OPENCODE_CONFIG_DIR/plugins/` (`01-C...:1`), and the project's `.opencode/plugins/` (`01-D...:1`). With none of
  them nothing loads (`01-E`: no probe file at all).
- **A plugin can be added without editing the user's config:** `OPENCODE_CONFIG_CONTENT` is merged over `opencode.json`
  (`01-B...` `config` hook line 3: `plugin_origins` source `OPENCODE_CONFIG_CONTENT`, scope `local`; the provider and model
  of the user's file stayed). It works in the TUI too (`tui-4...:1`, options `{"from":"config-content"}`). `OPENCODE_CONFIG_DIR`
  is a second no-edit way: its `plugins/` is scanned (`01-C...:1`); that it also reads an `opencode.json` there is from the source
  (`config/config.ts:432-445`), not tested.
- **Side effect to know:** any config dir that has plugins gets `package.json`, `package-lock.json`, `.gitignore` and a
  `node_modules/` with `@opencode-ai/plugin`, `@opencode-ai/sdk`, `zod`, `effect`... installed in the background (`npm`
  install, needs network): `01-A-plugins-dir.files.txt`, `01-C-config-dir.cfgdir-files.txt`. This happens in the dir the plugin
  is in, so for the user's config dir it would write there; with `OPENCODE_CONFIG_DIR` pointing at a piggery-owned dir it writes
  there. A plugin file with no imports does not need it.
- **Process and thread:** the plugin runs in the TUI process, not the main thread but the **worker thread** that hosts opencode's
  server (`main:false, tid:1, argv ["bun","/$bunfs/root/src/cli/tui/worker.js"]`, `01-F-tui-noport.probe.jsonl:1`).
  `process.env` is the TUI's environment (the worker gets a copy).
- **`client` reaches that same server in every mode.** `opencode serve`: `http://127.0.0.1:4101/session?...`
  (`01-A...:4`). TUI without `--port`: `client` works through an in-process `fetch` (response url `""`,
  `01-F...:53`, `tui-4...:4`) and **`input.serverUrl` is a lie there**: `http://localhost:4096/` while nothing listens
  (`01-F...:1`); use `client`, never `serverUrl`. TUI with `--port 4110`: `serverUrl` is the real listener
  (`tui-3...:1`), and `curl` reaches the same instance (`tui-2` abort by API worked).
  Note `client` is the **v1 SDK**: `client.global.health` does not exist (`01-A` first run), `client.session.*`, `client.path.get`,
  `client.event` do (`.../probe.js` logs the keys, `01-A...:2`).
- **One TUI start = ONE `plugin()` call without `--port`, but TWO with `--port`:** `tui-3...:1` and `:4` (`inst` 1 and 2, same
  directory, same thread, 2 ms apart; `01-F`/`tui-4` have one). Only the second receives every event and hook
  (`jq -r 'select(.kind=="hook" or .kind=="event")|.inst' tui-3...|sort|uniq -c`: 645 lines `inst:2`, one `inst:1` (its `config`
  hook), two `inst:3` (the other directory)) and it is never disposed (no `dispose` line for it), so its timers and connections leak. I did not find why
  (only `--port` differs: the TUI then also calls the worker's `server` RPC, `cli/tui.ts:234-241`; not traced further).
  **Consequence: `plugin()` must not open the piggery connection itself; do it lazily at the first hook/event, and
  tolerate an abandoned instance.**
- **One instance per project directory:** events and hooks reach only the instance of their directory
  (`plugin/index.ts:255-259` filters on `event.location.directory`). A session created in `/tmp/oc/work2` through the same
  server started a third `plugin()` (`tui-3...:564`, directory work2) that saw only its own `session.created` (567).

### 2. Event order and turn identity

Events seen by the plugin's `event` hook (`{id, type, properties}`); noise to filter by `type`: `plugin.added`, `catalog.updated`,
`reference.updated`, `integration.updated`, `file.*`, `session.diff`.

- **Plain turn** (`tui-1...:60-171`): `session.created`(60) → `chat.message` hook(61) → `message.updated` user(63) → `message.part.updated`
  text → `session.updated` → `session.status busy`(66) → `message.updated` assistant(67, `parentID` = the user message) →
  `experimental.chat.system.transform`, `chat.params`, `chat.headers` → ... deltas → `message.updated` assistant with `finish:"stop"`(×2) →
  `session.status busy` (again) → `session.status idle`(167) → `session.idle`(168) → `session.updated`/`session.diff` → one more
  `message.updated` user (summary refresh).
- **Tool turn** (`tui-1...:172-281`): one assistant message per LLM step, all with the same `parentID` = the user message:
  step 1 `finish:"tool-calls"` (244), `tool.execute.before/after` hooks around each tool (225, 232-236, 240), a `busy` before each next
  step, step 2 `finish:"stop"` (277/278), `busy`(279), `idle`(280), **`session.idle`(281): one per settled loop run**, not per step.
  `session.status busy` is emitted several times per turn (once per step and once right before idle): **never treat `busy` as turn
  start; take the first user message / `chat.message` for that.**
- **Steer: a prompt while busy** (`tui-1...:285-391`; TUI showed `QUEUED`): the second user message is created at once while the first
  tool is still running (`chat.message` 321, `message.updated` user 323), no new busy/idle; when the tool ends the loop starts step 2 whose assistant
  message has **`parentID` = the steer message** (334), reads both user messages, answers with the steer applied ("first done
  BANANA"), then **one** `session.idle` (388) for both. So a settled run can answer several user messages; "which turn is this idle
  for" = the set of user message ids since the last idle (from `chat.message`/`message.updated` role user) and the `parentID`s of the assistant
  messages (`message.updated` role assistant). `session.idle` and `session.status` carry only `sessionID`.
- Through the plugin's own client (what piggery will do), while busy, with `--port` and without: same result
  (`tui-4...:147-216`, "np first. MANGO"; `tui-3...:615-695`).
- The `experimental.chat.messages.transform` hook (`input` is `{}`, `output.messages[].info.sessionID`) runs once per LLM step
  of the main call and shows what the model sees: unanswered steer messages are in it (`tui-2...:343` onward lists them).

### 3. `experimental.chat.system.transform`

- **Input is only `{sessionID, model}`** (`tui-1...:68`; `agent` is not there). `sessionID` was always set. `output.system` is a
  `string[]` holding the whole system prompt as one 91K string (+ the title agent's, 2K); pushing a string appends a second entry.
- **An added string reaches the model** and is **per session** because the hook gets the session: the probe added
  `PINEAPPLE-<last 6 of sessionID>`; the model answered `PINEAPPLE-grqcEU` (`tui-1...:162`), `PINEAPPLE-ANJbNb` for a second session
  (`tui-3...:320`), `PINEAPPLE-mj3Dgz` for a served third (`:377`). Child sessions get it too (`tui-3...:457`, 493).
- **Trap: it also runs for the title generator**, in the same session, with the same input shape: `tui-1...:68` has
  `system_heads[0] = "You are a title generator..."` and the added string; the title of the session became `PINEAPPLE-grqcEU`
  (`10-sessions.txt`). The call before the real one cannot be told apart by input; `chat.params` (which has `agent:"title"`,
  `tui-1...:69`) comes right after it, and the title system string starts with `You are a title generator`. Decide: match that, or only
  append once the session has a title, or accept the cost. Not tested: other internal agents (compaction, summary).

### 4. Plugin tool

- `execute(args, ctx)`: **`ctx.sessionID` is there**, with `messageID, callID, agent, abort, directory, worktree, metadata, ask, messages, extra`
  (`tui-1...:234`; `extra.model` is the full model). In a child session it is the **child's** id and `agent:"general"`
  (`tui-3...:465`): the tool is offered to subagents too, so `execute` must refuse a session whose `parentID` is set.
- The name the model sees is the key of the plugin's `tool` object, verbatim: `probe_whoami` (`tool.definition` toolID, the first 14 calls of
  `tui-1...:72-85`: `invalid question bash read glob grep edit write task webfetch todowrite skill probe_whoami probe_sleep`;
  the tool part says `tool:"probe_whoami"`, 228). Prefixing exists only for files in `tool(s)/` (`registry.ts:195`).
- Args given as **raw JSON-schema objects (no zod import) work** (`probe_whoami` was called with `{text:"x"}`; opencode takes the `legacyJsonSchema`
  path, `registry.ts:125-135`; all keys become required). Importing `@opencode-ai/plugin`/`zod` only resolves from a dir where opencode
  installed them (Q1).
- `ctx.abort` fires on abort (`tui-2...:245`, `aborted:true`).

### 5. Wake from the plugin

- **`client.session.promptAsync({path:{id}, body:{parts:[...]}})` into an idle session starts a turn**: status 204 in 22 ms
  (`tui-2...:65`), the user message exists within 4 ms (62), `busy` (66), the answer ("woke1"), `session.idle` (166). It is **shown in the TUI**
  as a normal user message and answer, with `--port` and without (`tui-4...:225`; `screens/tui-4-after-wake.txt`).
- **`client.session.prompt({..., body:{noReply:true}})` adds the user message and starts nothing**: 200 with the message in 16 ms
  (`tui-2...:175`), no `busy`/`idle`, shown in the TUI (`screens/tui-4-after-noreply.txt`); the next turn sees it in its history.
- While busy `promptAsync` is the steer path (Q2). `session.prompt` (without noReply) would block until the turn ends; not used.
- A command that `client.session.*` makes from an instance of **another directory** also worked on a session of the first
  (`tui-3...:626`, 204): the session routes are global by id. That is why the first steer capture has the probe's command run
  twice (two instances both polled the file): user messages `kcX0dh` and `Cu1pAY` (`tui-3...:615,621`); a probe artefact, fixed
  before `tui-4`.
- Not tested: a `promptAsync` in the tiny window between the last step's `finish:"stop"` and `session.idle`.

### 6. Several sessions, children

- Several root sessions in one process: **every hook and event carries `sessionID`** (`message.updated` also in `info.sessionID`);
  the TUI's `/new` (`tui-3...:252` `session.created` ANJbNb) and a served one (`:330` mj3Dgz via `POST /session`, prompted by
  `prompt_async` while the TUI was on another session) both reach the same plugin instance and have their own `idle` (326, 383).
  A session in another directory goes to another plugin instance (Q1).
- **Root vs child: `parentID`.** `session.created` has `properties.info.{id, parentID, directory, title}`: `parentID:null` for roots, the parent's id
  for the task tool's child (`tui-3...:427`, "Probe whoami task (@general subagent)"). The child has its own `chat.message` (430, `agent:"general"`),
  `system.transform`, `chat.params`, tool calls and `session.idle` (510), all with the child's id, **before** the parent's idle (556).
  `client.session.get({path:{id}})` → `parentID` (`tui-3...:560`), `client.session.children`, `session.list()` (all sessions of the directory, roots
  and children, with `parentID`, 562) and `session.status()` (busy sessions only; `{}` when all idle, 563) give the same without events.
- **A session that predates the plugin (TUI `--session X`, `--continue`) emits no `session.created`** (`tui-4...` starts with `chat.message`
  for a session first seen there): look unknown ids up with `session.get` before the first use.
- Hooks do not carry the root: for a child, `chat.message.input.agent` is the subagent (`general`), but custom agents make the name unreliable; use `parentID`.

### 7. Abort

- **Esc Esc in the TUI and `POST /session/:id/abort` (returns `true`) give the same events** (`tui-2...:242-251` and `331-340`):
  `session.error` `{sessionID, error:{name:"MessageAbortedError", data:{message:"Aborted"}}}` (242) → `session.status idle` (243) → **`session.idle` (244)**
  → the running tool's `ctx.abort` fires, the tool returns (245) → tool part `completed` with its output (248) → `message.updated` of the
  assistant message **with `error:{name:"MessageAbortedError", data:{message:"Aborted"}}` and tokens 0** (249) → `session.status idle` + **a second
  `session.idle` (250/251)**. So **abort gives two idles** about 200 ms apart (the first before the tool and message are finished): do not
  wake the session at the first one; wait for the aborted assistant message (`error`) or the second idle.
- Abort of an idle session returns `true` and still emits `status idle` + `session.idle` (`tui-2...:341-342`): an idle event is no proof a turn ended.
- **The pending steer message stays unanswered**: its user message exists (`chat.message` 237, `message.updated` 239) and was not consumed; after the
  abort the TUI shows it with no answer. It is **not** removed or re-queued: the next turn's history contains it
  (`tui-2...:343` `messages.transform` lists `user:ZikXiM` before the new one). It did not make the model answer it separately
  ("after" only), it is just context.
- The aborted assistant message has `finish` absent and `error` set; the tool part is `completed` (not error) with the tool's own text.

### 8. Permission

- `{"permission":{"edit":"ask"}}` (via `OPENCODE_CONFIG_CONTENT`): a write → **`permission.asked`** `{id:"per_...", sessionID, permission:"edit", patterns, metadata:{filepath, diff},
  always:["*"], tool:{messageID, callID}}` (`tui-3...:152`), the TUI shows `Allow once / Allow always / Reject` and the turn waits (no `session.status` event
  between 152 and 153); Enter → **`permission.replied`** `{sessionID, requestID, reply:"once"}` (153) → `file.edited`, the tool runs, the turn goes on.
- **The `permission.ask` hook never fires in 1.18.34**: `flags.json` had `perm:"allow"` and the hook is registered, yet there is no `permission.ask` line in
  `tui-3...` (`rg -c '"permission.ask"'` = 0) and the permission was asked anyway. In the source it is only a type
  (`packages/plugin/src/index.ts:261`; no `trigger("permission.ask")` anywhere in `packages/opencode/src`). A plugin can only
  watch the two events. Not tested: replying through the API (`client.permission`/`POST .../permissions/:id`) or `reject`.

### 9. Model, variant, usage

- **A model or variant change in the TUI emits no event and no hook** (`/models` choice and `ctrl+t` variant, the TUI shows `glm-b ... · low`):
  `tui-3...:201` → `202` are 129 s apart with nothing between. The new model arrives with the **next user message**: `chat.message` input
  `{sessionID, agent, model:{providerID:"hp", modelID:"glm-b"}, variant:"low"}` (`:203`), the user message `info.model:{providerID, modelID, variant}` (227),
  `chat.params`/`system.transform` `model:{id, providerID, limit:{context, output}, variants}` (229-230, `chat.params` output `options.reasoningEffort`),
  and every assistant `message.updated` has top-level `modelID`, `providerID`, `variant` (244). The session row in `opencode.db` has `model`/`agent` columns too (`10-db-schema.txt`).
  The model's context size is `model.limit.context` in those hook inputs.
- **Usage per assistant message** (`message.updated`, role assistant): `tokens:{total, input, output, reasoning, cache:{read, write}}` and `cost`: e.g. `{total:28339, input:102,
  output:24, reasoning:53, cache:{write:0, read:28160}}` (`tui-1...:244`). Written on each step's final update, `0`s on an aborted message (`tui-2...:249`).
  ctx for a session = the latest assistant message's `tokens.total` over `model.limit.context`; `total` already includes the cache reads.
  The TUI's own figure matched when read on screen (28,237 tokens, 6% for the first session, the same 28237 as `10-turns-ctx.txt`; no screen file kept).

### 10. Persistence

- `$XDG_DATA_HOME/opencode/opencode.db` (SQLite, WAL: `opencode.db-wal`), readable **while the TUI runs** (`10-persist.sh` opens it `-readonly`).
  Tables: `session` (id, parent_id, directory, title, `agent`, `model`, token and cost totals, time_*), `message` (id, session_id, `data` JSON with role,
  `tokens`, `modelID`, `variant`, `finish`, `error`, `parentID`), `part` (`data` JSON: `type` text/tool/reasoning..., `text`, `tool`, `state`), `permission`, `todo`, `event`, `session_message`... (`10-db-tables.txt`, `10-db-schema.txt`).
- **tail, ctx, turns without the daemon are all one SQL away**: turns = `message` rows with `json_extract(data,'$.role')='user'`; ctx = last assistant `tokens.total`; last model and variant;
  tail = last `part` rows of type `text`/`tool` (`10-turns-ctx.txt`, `10-tail.txt`, `10-last-messages.txt`, `10-sessions.txt`). Children have `parent_id`.
  Time columns are epoch ms.
- CLI readers need no model: `opencode session list` (`10-cli-session-list.txt`), `opencode export <id>` (full JSON incl. messages and parts,
  `10-cli-export-head.txt`), `opencode stats`. The DB is the only thing that persists sessions: opencode's own session ids (`ses_...`) are what piggery would store to resume
  (`opencode --session ID`, `--continue`; used in `tui-4` and `tui-2`).

## Could not answer / not done

- Why `--port` makes two plugin instances (Q1), and whether the first is ever disposed: seen, not explained.
- `promptAsync` in the gap between the last step and `session.idle` (Q5); two sessions busy at the same moment; compaction events (`experimental.session.compacting`);
  the hook for slash commands (`command.execute.before`) and `shell.env` were hooked but not exercised except `shell.env` (once, bash).
- Replying to a permission from the plugin or by API, and `reject` (Q8).
- Which internal agents besides `title` call `system.transform` (Q3).
- TUI keys and screens differ by terminal; only tmux 200x50 was used. The TUI had no model switch event to observe because the TUI keeps model state locally.
- `opencode` 2.x, Windows: out of scope.

Publication: machine paths in these files were rewritten in place (home -> /home/user, checkout -> /work/piggery, the two run dirs and the macOS temp prefix -> /tmp/oc and /tmp), line numbers unchanged. The scripts therefore name one run dir, /tmp/oc, where they were run in two.
