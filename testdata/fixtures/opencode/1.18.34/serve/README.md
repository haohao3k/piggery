# opencode 1.18.34: the headless worker transport (opencode capture capture b)

Evidence only, no piggery code. Binary: `opencode-ai` 1.18.34 (`/tmp/oc/pkg`), source 907b3bc (`/tmp/opencode-src`).
Model: `hp/glm-5.3-flash` only. About 36 model requests in all (some turns have two: a tool step and a final step); the
count is above the ~30 asked because 07, 06b and 09d were rerun after script bugs (see "Reruns").

Every `.txt` is the output of the script with the same prefix. File:line below are into these captures.
Nothing in this directory contains the provider key (`redact` in `lib.sh` replaces it; checked by a byte scan of all files).
`~/.config|.local/share|.cache|.local/state/opencode` were not touched (mtimes and a `find -newer` check, before/after).

## Setup (what the scripts expect)

```
cp -r /tmp/oc/xdg /tmp/oc/xdg                 # scratch XDG dirs, never the user's
sed 's|^O=/tmp/oc$|O=/tmp/oc|; s|PATH=\$O/pkg|PATH=/tmp/oc/pkg|' /tmp/oc/env.sh > /tmp/oc/env.sh
./NN-name.sh                                    # each script writes NN-name.txt (+ its raw files) next to itself
```

The provider is `hp` in the scratch `opencode.json`, its key comes from `HP_KEY` through `{env:HP_KEY}`. A serve is started
with `OPENCODE_SERVER_PASSWORD=pw` (dummy) in every script except 01. `lib.sh` has the helpers (start serve and read the
port, `api`, `descendants`, SSE start/stop, `wait_idle`, `redact`). Pids started are in `/tmp/oc/pids` and were all
stopped by pid.

## Files

| Script | Capture | What |
|---|---|---|
| `00-openapi-paths.sh` | `00-openapi.txt` | routes of this serve (`GET /doc`), body schemas of `POST /session` and `prompt_async` |
| `01-start.sh` | `01-start.txt`, `01-start-*.log.out/.err` | port discovery, start time, the 4096 preference |
| `02-lifecycle.sh` | `02-lifecycle.txt`, `02-lifecycle-*.out` | auth; SIGTERM/SIGINT/SIGHUP/SIGKILL; children left behind (`fake-mcp.py`, a `/shell` sleep) |
| `02b-parent.sh` | `02b-parent.txt` | parent death (`parent.py` stands in for the daemon), stdin EOF |
| `03-prompt.sh` | `03-prompt.txt`, `.sse.jsonl`, `.A.messages.json`, `.B.sync-response.json` | session create, `prompt_async`, SSE end-of-turn, sync `/message` |
| `04-steer.sh` | `04-steer.txt`, `.sse.jsonl`, `.messages.json` | second `prompt_async` while busy |
| `05-abort.sh` | `05-abort.txt`, `.sse.jsonl`, `.messages.json` | `POST /abort` mid tool call, a prompt queued behind it |
| `06-run.sh` | `06-run.txt`, `06-run.r*.jsonl` | `opencode run --format json` per batch, `--session`, `--attach`, two runs on one session |
| `06b-run-abort.sh` | `06b-run-abort.txt`, `.r4/.r5.jsonl` | SIGINT / SIGTERM of a `run` mid tool call |
| `07-resume.sh` | `07-resume.txt`, `.before/.after.messages.json` | new serve continues the session; serve SIGKILLed mid tool call |
| `08-models.sh` | `08-models.txt`, `.config-providers.json`, `.provider.json`, `.proxy.jsonl` | models list, model/variant/system per prompt, what reached the API (`proxy.py`) |
| `08b-bad-model.sh` | `08b-bad-model.txt`, `.sse.jsonl` | a prompt naming a missing model |
| `09a-config-merge.sh` | `09a-config-merge.txt`, `.S*.config.json` | config / `OPENCODE_PERMISSION` / `OPENCODE_CONFIG_DIR` merge (no model call; fixtures in `cfg-fixtures/`) |
| `09b-tools.sh` | `09b-tools.txt`, `.proxy.jsonl` | tools the model really gets; config deny and per-session deny |
| `09c-noreply.sh` | `09c-noreply.txt` | `noReply:true` and a client-chosen `messageID` (no model call) |
| `09d-permission-ask.sh` | `09d-permission-ask.txt` (+ `.try1-model-skipped-tool.txt`) | a permission `ask` in a headless session |
| `09e-isolation.sh` | `09e-isolation.txt` | user skills / providers that leak into a worker (no model call) |
| `09f-worker-env.sh` | `09f-worker-env.txt`, `.proxy.jsonl` | the proposed worker env, end to end |
| `10-volume.py` | `10-volume.txt` | SSE volume per event type and the compact-log field map |
| `11-ignored-switch.sh` | `11-ignored-switch.txt`, `.proxy.jsonl`, `.messages.json` | a `noReply` prompt with one synthetic+ignored text part switches the session's model, makes no model request, and the text is nowhere in the next LLM request (`proxy.py PROXY_NEEDLE`; the committed proxy has a placeholder upstream, so that call fails and costs nothing) |
| helpers | `lib.sh`, `sse.py`, `proxy.py`, `msgs-summary.py`, `parent.py`, `fake-mcp.py` | |

## Answers

### 1. `opencode serve`: port, auth, start, signals, children

- **Port.** `--port 0` is the default and it is *not* "any free port": it tries 4096 first and only then a random one
  (source `server/server.ts:118-121`). First serve got 4096, the second 59684 (`01-start.txt:3,5`). The only
  machine-readable signal is one stdout line `opencode server listening on http://127.0.0.1:<port>`
  (`01-start-A.log.out:2`, source `cli/cmd/serve.ts:20`). Without a password it is preceded by the line
  `Warning: OPENCODE_SERVER_PASSWORD is not set; server is unsecured.` (`:1`), so parse the line that contains
  `listening on`, not the first line. Safer: the driver picks its own free port with `--port N` (`01-start.txt:7`), and
  still reads the line to know it is up. Default host is `127.0.0.1`.
- **Auth.** `OPENCODE_SERVER_PASSWORD` turns on HTTP basic auth: user `opencode`, or `OPENCODE_SERVER_USERNAME`. No
  credentials, wrong user or wrong password: 401; also on `/event` (`02-lifecycle.txt:4-10`). Without it the server is
  open. The password goes in the environment, not argv. `GET /config` and `GET /config/providers` return the provider
  `apiKey` resolved (`08-models.config.json`, redacted here), so the password is the only thing protecting the key.
- **Start time.** 660-900 ms from exec to the listening line (`01-start.txt:3,5,7`; `02-lifecycle.txt:2,13`). The first
  request for a directory boots the instance (about 45 `plugin.added` events, `10-volume.txt:2`): the first turn costs
  about 1 s more (2.7 s then 1.4-2.4 s, `03-prompt.txt:10,14`; `08-models.txt:76-79`).
- **Signals.** SIGTERM, SIGINT and SIGHUP end it in 140-220 ms (`02-lifecycle.txt:20,33,47`); no graceful path (the shell
  reports death by signal). Note: a bash background job starts with SIGINT ignored; `lib.sh` resets it before exec, or the
  SIGINT test is meaningless.
- **Parent death.** Nothing happens: after the parent was SIGKILLed (pipes on stdin/stdout/stderr, like the runner) the
  serve is re-parented to pid 1 and keeps answering and creating sessions (`02b-parent.txt:7-9`). **stdin EOF is ignored**
  (`02b-parent.txt:17`). So a crashed daemon leaves live serves behind: the driver has to record pid + port + password and
  kill or re-adopt them in recovery.
- **Children.** A stdio MCP server is in the serve's process group and exited on all four endings, but only because my fake
  exits on stdin EOF (`02-lifecycle.txt:21-22,34-35,48-49,61-62`); one that ignores EOF would stay (not tested). **Shell tool
  children are not cleaned up**: the bash tool runs `zsh -l -c '... eval "<cmd>"'` detached (own process group), and after
  SIGTERM, SIGINT, SIGHUP and SIGKILL the `zsh` and its `sleep` were still alive (`02-lifecycle.txt:21,34,48,61`; listing
  with pgids at `:15-17`). They are only killed by `POST /session/:id/abort` (`05-abort.txt:9`). Stop = abort every busy
  session first, then kill the serve, then kill what is left under it (walk the ppid tree before killing; the shells have
  ppid 1 afterwards).

### 2. `opencode run --format json --session <id>` per batch vs serve

| | `run` per batch | serve + `prompt_async` + SSE |
|---|---|---|
| latency of one tiny turn | 3.6 s new / 3.1 s resumed (`06-run.txt:2,5`); `--attach` to a serve 2.65 s (`:28`) | 1.4 s sync warm (`03-prompt.txt:14`); 1.7-2.7 s to idle (`03:10`, `08-models.txt:76-79`); 204 in 9 ms (`03:8`) |
| steer | impossible: no stdin, and a second `run` has its own in-process server, so the first does not see it: `/session/status` on a serve showed `{}` while a run was inside a tool call (`06-run.txt:52`); the second run answered its own message and **the first run's final reply never happened** (`:55-66`: tool-calls assistant, then only SECOND) | yes, see 4 |
| abort | SIGINT: exit 130 in 156-196 ms; SIGTERM: exit 143. **The tool child survives** (`06b-run-abort.txt:4,17`; `06-run.txt:31,49`), the session is left with an assistant message that never finished and a tool part `running`, no abort error (`06b:10-13`) | `POST /abort` in 40 ms, kills the tool child, marks `MessageAbortedError` (`05-abort.txt:7-9,17`) |
| tail / ctx / usage | the json lines (`step_start`, `text`, `step_finish` with tokens; `06-run.r1.jsonl`) only at step ends | SSE, per part and per step |
| model / variant / system per batch | `--model`, `--variant`, no system | in the body of every prompt |
| session memory | same sqlite store, so `--session` remembers: PLUM42 in a new process (`06-run.txt:5-6`) | same |
| process | one per batch; nothing to recover | one long-lived process to own (see 1) |

**Recommendation: one `opencode serve` per worker (loopback, random port, password in env), `prompt_async` plus one SSE
`/event` stream.** Reasons: steering and a clean abort exist only there (04, 05); no boot cost per batch; the SSE stream
gives the tail, ctx and the end-of-turn signal; model, variant and system are per prompt. Cost: the driver owns a process
that outlives its parent (1) and leaves shell children (1). `run --attach` is not a middle way: it has the same one-shot
shape and the same SIGINT behaviour.

### 3. Session id

- **piggery cannot choose it.** `POST /session` has `parentID, title, agent, model, metadata, permission, workspaceID`
  and no `id` (`00-openapi.txt`, the `POST /session` schema at the end). A body with `"id":"ses_mine_0001"` was accepted and
  the id ignored: the session got `ses_ef558709cffeUAyUM1YrfJDLSu` (`03-prompt.txt:2-3`). The driver stores opencode's
  `ses_...` next to its run id. `parentID` makes a child session (the plugin must not treat children as workers).
  A user message id *can* be chosen: `messageID` (pattern `^msg`) in the prompt body is kept (`09c-noreply.txt:2,7`), which
  gives deterministic correlation of a mail batch with the stored message.
- **Memory across processes.** The store is sqlite in the XDG data dir, shared by all processes: a session created by `run`
  is visible and continued in a serve (`06-run.txt:9-26`), and after the serve was SIGKILLed in the middle of a tool call, a
  new serve on a new process continued the same session and answered PLUM42 (`07-resume.txt:5-29,49`). What a crash
  leaves: the assistant message has no `finish` and its tool part stays `running` forever (`07-resume.txt:27`); the new
  serve reports the session idle (`:12`), and the model coped with the dangling call.

### 4. `prompt_async` + SSE `/event`

- **Stream.** First event `server.connected`, then bus events, `server.heartbeat` every 10 s (`04-steer.sse.jsonl`,
  source `handlers/event.ts:63-71`). No replay: connect before prompting, and after a reconnect resync with
  `GET /session/status` + `GET /session/:id/message`. The directory comes from the `x-opencode-directory` header.
- **End of turn.** `session.status {status:{type:"idle"}}` immediately followed by `session.idle` (`03-prompt.txt:107-108`;
  `05-abort.txt:102-103`). `busy` is re-sent several times, also right before `idle` (`03-prompt.txt:107`), so take the last
  status. `GET /session/status` lists only busy sessions (`{}` = idle, `03-prompt.txt:6,9,11`) but a poll right after the 204
  can miss the start of the first turn on a fresh instance (my first `wait_idle` did): subscribe to SSE first.
- **A second `prompt_async` while busy** (`04-steer.txt`): 204, the user message is stored at once (`:15`, +6.0 s) but not
  applied inside the running tool call; the tool ran to its end (+14.9 s), then the *same* loop made its next model call
  with both user messages and answered **once**: `ONE TWO-STEERED` (`:17-20`); two assistant messages in total (the
  tool-calls step and the final one). No second loop, no double answer. Granularity is "next step", not "next token".
- **Abort** (`05-abort.txt`): `POST /session/:id/abort` returns `true` in 40 ms (`:7`); events `session.error
  {name:"MessageAbortedError"}`, `session.status idle`, `session.idle` (`:101-103`), a second `idle`+`idle` pair a few ms
  later (`:106-107`); the assistant message carries `error.name = MessageAbortedError` (`:17`); the `sleep` child is dead
  (`:9`). Abort on an idle session also returns `true` and emits `idle` again (`:10,108-109`). **A prompt queued behind the
  aborted turn was not run, but stays in the transcript as an unanswered user message** (`:21-22`); the next prompt's
  answer (`:25-28`) only answers the newer one. So "abort keeps unread mail" cannot be done by opencode itself: the mail
  already steered in is in its history (`DELETE /session/:id/message/:id` exists in `00-openapi.txt`, not tried).
- **Sync `POST /session/:id/message`** blocks to the end of the turn and returns `{info, parts}` of the assistant message
  (`info.finish`, `info.tokens`, `info.error`; `03-prompt.txt:14`, `03-prompt.B.sync-response.json`). With a bad model it
  returns a 500-style `UnknownError` with no detail (`08b-bad-model.txt:6`), whereas SSE carries the reason. Not tested on a
  multi-step turn (which message it returns). Use `prompt_async` + SSE.
- **`noReply:true`** stores the user message with no model call and no turn (`09c-noreply.txt:3-6`): a way to deposit mail
  into the transcript without waking.

### 5. Model per prompt, variant, models list

- **List.** `GET /config/providers` -> `{providers:[{id,name,source,env,models:{<id>:{name,variants?,limit:{context,output},
  capabilities,...}}}], default:{<provider>:<model>}}` (`08-models.txt:67-69`). It lists only configured/connected
  providers, which includes opencode's own hosted provider `opencode` (free models) even though nobody configured it;
  `"enabled_providers":["hp"]` reduces it to `hp` (`09e-isolation.txt:12-14`). `GET /provider` returns all 227 models.dev
  providers plus `connected` (`08-models.txt:70-71`; the file is trimmed to the connected ones, see Reruns).
  `opencode models [provider] [--verbose]` prints the same without a server (`08-models.txt:1-3` and the `--verbose` JSON after it). Model id for
  `set_model` = `provider/model` (`hp/glm-5.3-flash`); variants of a model are `models[].variants` (keys).
- **Per prompt.** `body.model = {providerID, modelID}` and `body.variant`. They are stored on the user message and used for
  that turn's steps (source `session/prompt.ts:1141`: each step calls `getModel(lastUser.model...)`). Switching is just the next
  prompt with another model: user/assistant model follow (`08-models.txt:126-136`, with `alt`, an alias of the same API model
  defined in the capture's config). Prompt without `model`: not tested.
- **Variant = reasoning.** With `variant:"high"` on a model that has variants the API request got `reasoning_effort:"high"`,
  `low` -> `low`, none -> no key (`08-models.txt:83-85`, from `proxy.py`). A variant on a model that has none is silently
  dropped from the request (`:86`) while the stored assistant message still says `variant: high`. Which models have variants
  depends on the model (source `provider/transform.ts:790-890`; plain `glm-5.3-flash` has none: `08-models.txt:69`). Valid
  levels must be taken from the models list. Changing reasoning effort also changed prompt-cache use in one case (`cache.read`
  0 after `low`, `08-models.messages.json`): minor.
- **Missing model.** `prompt_async` still answers 204; the user message is stored, no assistant message is made, and SSE
  carries `session.error {name:"UnknownError", data.message:"Model not found: hp/nonexistent."}` then `idle` (a second,
  stack-trace `session.error` follows) (`08b-bad-model.txt:12-27`). The failed user message stays in history and is sent
  to the model with the next prompt (`08-models.txt:87`, msgs=11). Check the id against the list before sending.
- **Per-prompt `system`** is *appended* to opencode's own system prompt (it is the last part, `08-models.txt:87`), it does
  not replace it. Usable for a role card, though the plugin's system transform is the story's route.

### 6. Worker config without touching the user's

Merge facts (`09a-config-merge.txt`, source order in `config.mdx:48-51`): the user's global config is always read, even
with `OPENCODE_CONFIG_DIR` (`:37-44`); `mcp`, `agent` merge by key, `plugin` arrays concatenate (`:10-17`, `:28-35`);
`OPENCODE_CONFIG_CONTENT` overrides global and dir (`:29`: `read` allow beat the dir's deny); `OPENCODE_PERMISSION` beats
`OPENCODE_CONFIG_CONTENT` (`:20`: bash allow won over deny); `"permission":"allow"` becomes a key `"*":"allow"`
(`:11`). **Rules are evaluated in object-key order, last match wins, and a key keeps the position of its first
appearance.** So `"*":"allow"` appended after the user's `bash:"ask"` wins (`:11`), but a user config whose `*` or specific
rule comes *later* than the injected one would still win: set every key you care about explicitly, do not rely on `*`
alone. `XDG_CONFIG_HOME` pointed at an empty dir hides the user's config completely (`:46-53`) but also hides their
provider config; the key then has to come in through `OPENCODE_CONFIG_CONTENT` (works, `:47-53`). Auth files live under
`XDG_DATA_HOME`, not touched here.

Tools the model gets (what really reached the API, `proxy.py`):

- default: `bash edit glob grep question read skill task todowrite webfetch write` (11, names in `08-models.proxy.jsonl:1`, count at
  `08-models.txt:83`; `apply_patch`, `websearch`, `invalid` are registered but not offered to this model, `09a-config-merge.txt:8`).
- `permission` with `"question":"deny","task":"deny"` in config removes them (9 tools, `09b-tools.txt:7`); the same by
  `permission` in the `POST /session` body, which works per session (8 tools with `webfetch` also denied, `09b-tools.txt:14`).
  Rule: a tool is hidden when its last matching rule is `deny` with pattern `*` (source `permission/index.ts:204-214`).
  `/experimental/tool/ids` ignores permissions (`09a`/`09b` ids unchanged), do not use it to check.
- Tools that reach the Human or start agents: **`question`** (asks the Human; on by default, source `tool/registry.ts:207`:
  only for client `app|cli|desktop`) and **`task`** (starts subagents). `skill` loads skills (see below); `webfetch`
  fetches URLs; `todowrite` is local; `plan_enter/plan_exit` only exist with the experimental plan mode flag.

All-allow: `permission: allow` / `{"*":"allow"}`. Without it a `bash:"ask"` rule **blocks the session forever**: the turn
stayed `busy` after 9 s and after a `once` reply to the first request the next ask (`bash`) blocked again (`09d-permission-ask.txt:3-13`;
events `permission.asked`/`permission.replied`; list `GET /permission`, reply `POST /permission/:id/reply {reply:"once|always|reject"}`).
Note the macOS `/tmp` vs `/tmp` path mismatch produced an `external_directory` ask there: a worker cwd must be the real path.

What else of the user's setup flows into a worker, and its switch (`09e-isolation.txt`, `09f-worker-env.txt`):

- **Skills.** 235 skills from `~/.claude/skills` and `~/.agents/skills` were listed in the system prompt (`09e:2`; 28 k tokens
  of context for a one-word turn, `08-models.txt:91`). `OPENCODE_DISABLE_CLAUDE_CODE=1` drops `~/.claude` (11 left, `09e:7`);
  `permission.skill: deny` removes the tool and the listing (source `session/system.ts:108`).
- **Instructions.** `instructions: [...]` of the user's config are injected into the system prompt (`09f-worker-env.txt:6`,
  tail shows the user's instruction file).
- **Providers.** see 5 (`enabled_providers`).
- **Share / autoupdate / LSP download.** `OPENCODE_DISABLE_AUTOUPDATE=1`, `OPENCODE_DISABLE_LSP_DOWNLOAD=1`, config
  `"share":"disabled"`, `"autoupdate":false`, `"lsp":false` (all read back through `GET /config`, `09a-config-merge.txt:16`).
  `OPENCODE_DISABLE_MODELS_FETCH=1` stops the models.dev refresh. At idle there was no non-loopback socket (`09e-isolation.txt:5`,
  weak evidence: one `lsof` sample after 4 s). `OPENCODE_DISABLE_DEFAULT_PLUGINS`, `OPENCODE_DISABLE_AUTOCOMPACT`,
  `OPENCODE_DISABLE_PRUNE` exist (`cli.mdx:684-700`), not exercised.

Proposed worker environment, run end to end against a user config that has `bash:"ask"` (`09f-worker-env.txt`): bash ran
with no ask (`:5`), the model got 8 tools without `question/task/skill` (`:6`), the turn used 6 k tokens instead of 28 k.

```
OPENCODE_SERVER_PASSWORD=<random>  OPENCODE_DISABLE_CLAUDE_CODE=1  OPENCODE_DISABLE_AUTOUPDATE=1  OPENCODE_DISABLE_LSP_DOWNLOAD=1
OPENCODE_PERMISSION='{"*":"allow","question":"deny","task":"deny","skill":"deny"}'
OPENCODE_CONFIG_CONTENT='{"share":"disabled","lsp":false,"enabled_providers":[...],"plugin":[<piggery plugin>]}'
```

### 7. Volume and the compact log

`10-volume.txt`. A one-turn session is 129-249 SSE events / 32-65 KB, of which `message.part.delta` is one per token
(146 events, 39 KB in the aborted run, `:38`) and `plugin.added` x45 + `catalog/reference/integration.updated` are instance boot
(`:2,19,39`). Keep: `message.part.updated` for `text` with `time.end` (full text), `tool` with `state.status` pending/running/
completed/error and `input`/`output`, `step-finish` (`tokens`, `cost`, `reason`); `message.updated` for the assistant message
(`tokens`, `finish`, `time.completed`, `error`, `modelID`); `session.status`/`session.idle`; `session.error`. Skip the rest
(field map and a worked example at `10-volume.txt:58-82`). Usage: `tokens = {total,input,output,reasoning,cache:{read,write}}`
with `total = input+output+reasoning+cache.read+cache.write` (`04-steer.txt:10`: 28137 = 131+25+77+27904); the last assistant
message's `total` is the context in use, the limit is `limit.context` in the models list. `cost` is 0 for a custom model.

## Not answered / not tested

- LSP child processes (needs an edit that starts an LSP server), an MCP server that ignores stdin EOF, Linux behaviour
  (this is macOS arm64).
- Prompt without `model` (which one is used), model change *inside* a running turn, `DELETE .../message/:id` to withdraw a
  steered message, `always` replies, per-session permission with `ask`, `OPENCODE_CLIENT` as a way to switch `question` off.
- Sync `/message` on a multi-step turn; SSE behaviour on reconnect beyond "no replay" (only `server.connected` is sent first).
- Many sessions in one serve and `x-opencode-directory` per session (each script used one directory).
- 1.18.34 also has an experimental `/api/session/...` surface (`prompt`, `model`, `interrupt`, `context`, `wait`;
  `00-openapi.txt:35-59`): not used or tested here; everything above is the v1 routes.
- Network silence of a worker: only the one `lsof` sample.

## Reruns and edits after capture (so scripts and outputs can be matched)

- `07-resume.txt` is the second run: the first had a `wait_idle` race (returned before the turn started), fixed in `lib.sh`.
- `06-run.txt`: its steps 5-6 wrote their files to the work dir by mistake (script fixed); they were rerun as `06b`.
  `06-run.r6a.*` were copied from the work dir.
- `09d-permission-ask.try1-model-skipped-tool.txt`: the model answered without calling the tool, nothing to ask; `09d` is
  the retry with a more explicit prompt.
- `09b-tools.txt` B3/B3b are void as evidence of an ask (nothing asked for `ls /etc/ssl`; the reply call hit the SPA
  fallback, whose HTML I cut out by hand and marked); B1/B2 are the valid parts, `09d` replaces B3.
- `08-models.provider.json` was 6.4 MB (227 providers): reduced after capture to `connected`/`default`/`all` of the
  connected providers (the same filter is now in `08-models.sh`, not rerun).

## Records golden (the contract between the driver's log and the plugin's)

`../records-golden/<capture>.jsonl` (03, 04, 05, 08b, 09d) are the standard records (`internal/driver/local/runner.go`) that
`opencodeRun.standard` in `internal/driver/local/opencode.go` makes from `<capture>.sse.jsonl`, for the session of the capture's
first `message.updated` (03 made another session first, from the create whose id opencode ignored). The plugin's
`records.mjs` makes the same lines for a TUI session. Regenerate with
`PGOLDEN=1 go test ./internal/driver/local -run TestOpencodeRecordsGolden` and review the diff; the test fails when the converter and a
file differ. Lines are compared as JSON values (Go writes keys sorted, no HTML escaping).
