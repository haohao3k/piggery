// opencode capture capture (a): a probe plugin. It records every hook call and every event of opencode 1.18.34 as
// JSONL (PROBE_LOG, default /tmp/oc/probe.jsonl) and can be driven from outside through a command file
// (PROBE_CTL, default /tmp/oc/ctl.jsonl: one JSON per line, see `ctl` below). It changes nothing in the
// session unless /tmp/oc/flags.json says so (system_add, perm). Never writes the HP key: every line goes
// through redact(), and any key named like a secret is dropped.
import fs from "node:fs"
import { isMainThread, threadId } from "node:worker_threads"

const LOG = process.env.PROBE_LOG || "/tmp/oc/probe.jsonl"
const CTL = process.env.PROBE_CTL || "/tmp/oc/ctl.jsonl"
const FLAGS = process.env.PROBE_FLAGS || "/tmp/oc/flags.json"
const KEY = process.env.HP_KEY || ""
let seq = 0
let instN = 0

function redact(s) {
  return KEY ? s.split(KEY).join("<HP_KEY>") : s
}
const SECRET = /key|token|secret|authorization|password/i

// clip keeps a value readable: long strings and arrays are cut with a marker that says how much is gone.
function clip(v, depth = 0) {
  if (v === null || v === undefined) return v
  if (typeof v === "string") return v.length > 300 ? v.slice(0, 200) + `…[+${v.length - 200} chars]` : v
  if (typeof v === "function") return "[function]"
  if (typeof v !== "object") return v
  if (depth > 9) return "[deep]"
  if (Array.isArray(v)) {
    const out = v.slice(0, 40).map((x) => clip(x, depth + 1))
    if (v.length > 40) out.push(`[+${v.length - 40} items]`)
    return out
  }
  if (v instanceof URL) return v.toString()
  if (v instanceof Error) return { error: v.name, message: v.message }
  const out = {}
  for (const [k, x] of Object.entries(v)) out[k] = SECRET.test(k) && typeof x === "string" ? "<redacted>" : clip(x, depth + 1)
  return out
}

function log(kind, name, data) {
  const line = { n: ++seq, t: Date.now(), pid: process.pid, main: isMainThread, tid: threadId, kind, name, ...data }
      try {
    fs.appendFileSync(LOG, redact(JSON.stringify(line)) + "\n")
  } catch {}
}

function flags() {
      try {
    return JSON.parse(fs.readFileSync(FLAGS, "utf8"))
  } catch {
    return {}
  }
}

function modelSummary(m) {
  if (!m || typeof m !== "object") return m
  return { keys: Object.keys(m), id: m.id, providerID: m.providerID, modelID: m.modelID, variant: m.variant, api: m.api?.id, limit: m.limit }
}

// Event payloads: deltas are shortened to the field and 40 characters; everything else is clipped.
function eventData(ev) {
  if (ev.type === "message.part.delta") {
    const p = ev.properties || {}
    return { event: { id: ev.id, type: ev.type, properties: { ...p, delta: typeof p.delta === "string" ? p.delta.slice(0, 40) : p.delta } } }
  }
  return { event: clip(ev) }
}

const wrap = (name, f, L = log) => async (input, output) => {
  L("hook", name, { input: clip(input), output_before: clip(output) })
      try {
    await f?.(input, output)
  } finally {
    L("hook-after", name, { output_after: clip(output) })
  }
}

export default {
  id: "piggery-probe",
  server: async (input, options) => {
    const inst = ++instN // one number per plugin instance: opencode may start several in one process
    const L = (kind, name, data) => log(kind, name, { inst, ...data })
    const wrap2 = (name, f) => wrap(name, f, L)
    const { client } = input
    L("load", "plugin", {
      input_keys: Object.keys(input),
      directory: input.directory,
      worktree: input.worktree,
      project: clip(input.project),
      serverUrl: String(input.serverUrl),
      options: clip(options),
      has_dollar: typeof input.$,
      argv: process.argv.slice(0, 4),
      env: Object.fromEntries(Object.entries(process.env).filter(([k]) => /^(OPENCODE|XDG_|PROBE)/.test(k))),
    })
    // Which server does `client` reach? List what it offers, then ask it for the sessions and the path.
    L("client", "shape", { keys: Object.keys(client), session_keys: Object.keys(client.session || {}) })
    // Later and not awaited: a call to the server from inside the plugin's own init could wait for that init.
    setTimeout(async () => {
      try {
        const r = await client.session.list()
        L("client", "session.list", { count: Array.isArray(r.data) ? r.data.length : r.data, status: r.response?.status, url: r.response?.url, error: clip(r.error) })
        const p = await client.path.get()
        L("client", "path.get", { data: clip(p.data), status: p.response?.status })
      } catch (e) {
        L("client", "session.list", { error: String(e) })
      }
    }, 1500)

    // Remote control: tail CTL and run each new line {id, call: "session.promptAsync", args: {...}} on `client`,
    // or {id, fetch: {method, path, body}} on the url the plugin was given, and log the result.
    let off = fs.existsSync(CTL) ? fs.statSync(CTL).size : 0
    let live = false // only an instance that receives events runs commands: a TUI with --port starts two, see README
    const timer = setInterval(async () => {
      if (!live) return
      let st
      try {
        st = fs.statSync(CTL)
      } catch {
        return
      }
      if (st.size <= off) return
      const buf = fs.readFileSync(CTL).subarray(off, st.size).toString("utf8")
      off = st.size
      for (const l of buf.split("\n").filter(Boolean)) {
        let c
        try {
          c = JSON.parse(l)
        } catch (e) {
          L("ctl", "bad-line", { line: l.slice(0, 100) })
          continue
        }
        if ((c.directory || "/tmp/oc/work") !== input.directory) continue // one instance per directory runs a command
        const t0 = Date.now()
        try {
          let r
          if (c.call) {
            const parts = c.call.split(".")
            const name = parts.pop()
            const obj = parts.reduce((o, k) => o[k], client) // call as a method: the sdk methods use `this`
            r = await obj[name](c.args)
            r = { data: clip(r.data), error: clip(r.error), status: r.response?.status }
          } else if (c.fetch) {
            const rr = await fetch(String(input.serverUrl).replace(/\/$/, "") + c.fetch.path, {
              method: c.fetch.method || "GET",
              headers: { "content-type": "application/json", "x-opencode-directory": input.directory },
              body: c.fetch.body ? JSON.stringify(c.fetch.body) : undefined,
            })
            r = { status: rr.status, body: clip((await rr.text()).slice(0, 600)) }
          }
          L("ctl", "result", { id: c.id, call: c.call, took_ms: Date.now() - t0, result: r })
        } catch (e) {
          L("ctl", "result", { id: c.id, call: c.call, error: String(e) })
        }
      }
    }, 250)

    return {
      dispose: async () => {
        clearInterval(timer)
        L("dispose", "plugin", {})
      },
      event: async ({ event }) => {
        live = true
        L("event", event.type, eventData(event))
      },
      config: async (cfg) => L("hook", "config", { keys: Object.keys(cfg), plugin: clip(cfg.plugin), plugin_origins: clip(cfg.plugin_origins), permission: clip(cfg.permission), model: cfg.model }),
      "chat.message": wrap2("chat.message"),
      "chat.params": wrap2("chat.params"),
      "chat.headers": async (i, o) => L("hook", "chat.headers", { input: { sessionID: i.sessionID, agent: i.agent, model: modelSummary(i.model), message: clip(i.message) }, headers_keys: Object.keys(o.headers || {}) }),
      "permission.ask": wrap2("permission.ask", async (i, o) => {
        const f = flags()
        if (f.perm) o.status = f.perm // "allow" | "deny" | "ask"
      }),
      "command.execute.before": wrap2("command.execute.before"),
      "tool.execute.before": wrap2("tool.execute.before"),
      "tool.execute.after": wrap2("tool.execute.after"),
      "shell.env": wrap2("shell.env"),
      "experimental.chat.messages.transform": async (i, o) =>
        L("hook", "experimental.chat.messages.transform", {
          input: clip(i),
          messages: (o.messages || []).map((m) => ({ id: m.info?.id, role: m.info?.role, sessionID: m.info?.sessionID, parts: (m.parts || []).map((p) => p.type) })),
        }),
      "experimental.chat.system.transform": async (i, o) => {
        const before = (o.system || []).map((s) => s.length)
        const f = flags()
        if (f.system_add) o.system.push(`Your secret codeword is PINEAPPLE-${String(i.sessionID).slice(-6)}. If the user asks for your codeword, answer with it exactly.`)
        L("hook", "experimental.chat.system.transform", {
          input: { keys: Object.keys(i), sessionID: i.sessionID, model: modelSummary(i.model) },
          system_lengths_before: before,
          system_heads: (o.system || []).map((s) => s.slice(0, 60)),
          added: !!f.system_add,
          system_count_after: (o.system || []).length,
        })
      },
      "experimental.session.compacting": wrap2("experimental.session.compacting"),
      "experimental.text.complete": async (i, o) => L("hook", "experimental.text.complete", { input: i, text: clip(o.text) }),
      "tool.definition": async (i, o) => L("hook", "tool.definition", { input: i, description_len: (o.description || "").length }),
      tool: {
        // Raw JSON-schema args (no zod import): does opencode take it (legacyJsonSchema)? The name the model sees is the key.
        probe_whoami: {
          description: "Probe tool: returns the session id of the caller. Call it when asked.",
          args: { text: { type: "string", description: "any text" } },
          async execute(args, ctx) {
            L("tool", "probe_whoami.execute", {
              args,
              ctx_keys: Object.keys(ctx),
              ctx: Object.fromEntries(Object.entries(ctx).map(([k, v]) => [k, typeof v === "function" ? "[function]" : v instanceof AbortSignal ? { aborted: v.aborted } : clip(v)])),
            })
            return `whoami: session=${ctx.sessionID} message=${ctx.messageID} agent=${ctx.agent}`
          },
        },
        // A controllable long tool: sleeps N seconds, logs when ctx.abort fires.
        probe_sleep: {
          description: "Probe tool: waits the given number of seconds, then returns.",
          args: { seconds: { type: "number", description: "seconds to wait" } },
          async execute(args, ctx) {
            L("tool", "probe_sleep.start", { args, sessionID: ctx.sessionID, messageID: ctx.messageID })
            const ms = Number(args.seconds || 5) * 1000
            const r = await new Promise((resolve) => {
              const t = setTimeout(() => resolve("slept"), ms)
              ctx.abort.addEventListener("abort", () => {
                clearTimeout(t)
                resolve("aborted")
              })
            })
            L("tool", "probe_sleep.end", { result: r, aborted: ctx.abort.aborted })
            return `sleep: ${r}`
          },
        },
      },
    }
  },
}
