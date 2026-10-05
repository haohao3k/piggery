// The plugin's interactive side (a TUI session): the real index.mjs against a fake daemon on a unix
// socket and a fake opencode client, fed the probe's captures as opencode sent them. A worker is
// worker.test.mjs (its own process: the plugin keeps per-process state).
import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { fakeDaemon, tempHome, until } from "../dsh/fake.mjs";
import { lines, play } from "./replay.mjs";

const T1 = "tui-1-port-turns-steer";
const T3 = "tui-3-port-permission-model-sessions";
const SID = "ses_ef55758b1ffekKdzea44grqcEU"; // tui-1's session
const CHILD = "ses_ef54f3927ffekI8MfMh6b87Hy4"; // tui-3's task-tool child session

test("a TUI session joins piggery: tools, role card, mail in, aborts, model, records", async (t) => {
	const home = tempHome();
	for (const k of Object.keys(process.env)) if (k.startsWith("PIGGERY_")) delete process.env[k];
	const mail = {}; // by participant: what the daemon hands it next
	const daemon = await fakeDaemon(home, (f) => {
		switch (f.verb) {
			case "join.auto":
				return { id: "p-" + f.args.harness_ref, token: "t1", run_id: "r1" };
			case "identify":
				return { team_id: "T", name: "lead", role: "peer", tools: ["send", "inbox", "who"], role_card: "team card", protocol_version: 1, run_id: f.args.run_id };
			case "harness.event": // the daemon hands mail at a turn start or boundary, and blocks an end that has some
			{
				if (!mail[f.auth.id] || !["turn_start", "tool_boundary", "turn_end"].includes(f.args.event) || (f.args.event === "turn_end" && f.args.outcome !== "ok")) return {};
				const text = mail[f.auth.id];
				delete mail[f.auth.id];
				return { text, block: f.args.event === "turn_end" };
			}
			case "send":
				return { seq: 7 };
			default:
				return {};
		}
	});
	t.after(() => daemon.close());

	// opencode's client (SDK v1), as far as the plugin uses it.
	const sessions = { [SID]: { id: SID, directory: "/tmp/oc/work", model: { providerID: "hp", id: "glm-5.3-flash", variant: "high" } } };
	const sdk = [];
	const logs = [];
	const client = {
		session: {
			get: async ({ path }) => {
				if (!sessions[path.id]) throw new Error("not found");
				return { data: sessions[path.id] };
			},
			promptAsync: async (a) => void sdk.push(["promptAsync", a.path.id, a.body]),
			abort: async (a) => void sdk.push(["abort", a.path.id]),
		},
		app: { log: async (a) => void logs.push(a.body.message) },
	};
	const records = join(home, ".piggery", "sessions", "opencode");
	const { default: plugin } = await import("./index.mjs");
	const hooks = await plugin.server({ client, directory: "/tmp/oc/work" }, undefined) // setup lists it with no options;
	t.after(() => hooks.dispose());
	const as = (verb) => daemon.calls.filter((c) => c.verb === verb);
	const events = () => as("harness.event").map((c) => c.args);

	await t.test("plugin() opens nothing: no connection before a session is seen", () => {
		assert.deepEqual(daemon.calls, []);
	});

	await t.test("a root session joins at its first message, by its session id, with the model it names and its records file", async () => {
		await play(hooks, lines(T1, 60, 61)); // session.created, chat.message
		assert.equal(as("join.auto").length, 1);
		const j = as("join.auto")[0].args;
		assert.deepEqual([j.harness, j.mode, j.harness_ref, j.cwd, j.tool_prefix], ["opencode", "interactive", SID, "/tmp/oc/work", "piggery_"]);
		assert.deepEqual(j.transcript, { path: join(records, SID, "records.jsonl"), format: "driver" });
		const id = as("identify")[0].args;
		assert.deepEqual([id.model, id.new_run, id.protocol_version], ["hp/glm-5.3-flash", false, 1]);
		assert.deepEqual(id.capabilities, ["abort", "wake", "steer", "system_prompt"]);
	});

	await t.test("the role card goes into the system prompt of that session only; the tools are the model's names with the real schema", async () => {
		const out = { system: ["base"] };
		await hooks["experimental.chat.system.transform"]({ sessionID: SID, model: {} }, out);
		assert.deepEqual(out.system, ["base", "team card"]);
		const tools = JSON.parse(readFileSync(new URL("../pi/tools.json", import.meta.url), "utf8")).tools;
		assert.deepEqual(Object.keys(hooks.tool).sort(), tools.map((f) => "piggery_" + f.name).sort());
		for (const f of tools) {
			const o = { description: "x", parameters: {}, jsonSchema: {} };
			await hooks["tool.definition"]({ toolID: "piggery_" + f.name }, o);
			assert.deepEqual(o.jsonSchema, f.parameters); // opencode's own would make every key required
			assert.doesNotMatch(o.description, /\{tool:/);
		}
		const other = { description: "keep", jsonSchema: 1 };
		await hooks["tool.definition"]({ toolID: "bash" }, other);
		assert.deepEqual(other, { description: "keep", jsonSchema: 1 });
	});

	await t.test("a tool call goes to the daemon as the session's participant; a tool its role lacks is refused", async () => {
		assert.equal(await hooks.tool.piggery_send.execute({ to: "boss", body: "hi" }, { sessionID: SID }), "sent #7");
		assert.deepEqual([as("send")[0].args, as("send")[0].as], [{ to: "boss", body: "hi" }, "p-" + SID]);
		await assert.rejects(hooks.tool.piggery_agent.execute({ action: "templates" }, { sessionID: SID }), /not available to your role/);
	});

	await t.test("a subagent's child session is nobody's participant: no join, no role card, no tools", async () => {
		await play(hooks, lines(T3, 427, 427)); // session.created with parentID
		await assert.rejects(hooks.tool.piggery_who.execute({}, { sessionID: CHILD }), /not a piggery participant/);
		const out = { system: ["base"] };
		await hooks["experimental.chat.system.transform"]({ sessionID: CHILD, model: {} }, out);
		assert.deepEqual(out.system, ["base"]);
		assert.deepEqual([...new Set(as("join.auto").map((c) => c.args.harness_ref))], [SID]);
	});

	await t.test("a session opencode never announced (--session, --continue) is looked up: a root joins, a child does not", async () => {
		sessions.ses_old = { id: "ses_old", directory: "/tmp/oc/old" };
		sessions.ses_kid = { id: "ses_kid", parentID: "ses_old", directory: "/tmp/oc/old" };
		const kid = { system: [] };
		await hooks["experimental.chat.system.transform"]({ sessionID: "ses_kid", model: {} }, kid);
		await hooks["experimental.chat.system.transform"]({ sessionID: "ses_old", model: {} }, { system: [] });
		assert.deepEqual(as("join.auto").map((c) => c.args.harness_ref), [SID, "ses_old"]);
		assert.equal(as("join.auto")[1].args.cwd, "/tmp/oc/old");
	});

	await t.test("a wake opens a run with the mail, under the session's variant and never a model", async () => {
		mail["p-" + SID] = "[piggery] 1 new message";
		daemon.push("wake");
		await until(() => sdk.some((c) => c[1] === SID));
		assert.deepEqual(sdk.find((c) => c[1] === SID), ["promptAsync", SID, { parts: [{ type: "text", text: "[piggery] 1 new message" }], variant: "high" }]);
	});

	await t.test("mail at a tool boundary is steered in as a prompt to the running session", async () => {
		sdk.length = 0;
		mail["p-" + SID] = "[piggery] 2 new messages";
		await play(hooks, lines(T1, 172, 284)); // a turn with tool calls
		await until(() => sdk.length === 1);
		assert.equal(sdk[0][2].parts[0].text, "[piggery] 2 new messages");
		assert.ok(events().some((e) => e.event === "turn_end" && e.outcome === "ok"));
	});

	await t.test("the abort push aborts the session", async () => {
		daemon.push("abort");
		await until(() => sdk.some((c) => c[0] === "abort" && c[1] === SID));
	});

	await t.test("a model or variant change shows at the next user message and is told to the daemon once; a driver's switch message is no record and no mail", async () => {
		const msg = (id, parts, model, variant) => hooks["chat.message"]({ sessionID: SID, agent: "build", model, variant }, { message: { id }, parts });
		const text = (s) => [{ type: "text", text: s }];
		await msg("msg_1", text("hello"), { providerID: "hp", modelID: "glm-b" }, "low");
		await msg("msg_2", text("again"), { providerID: "hp", modelID: "glm-b" }, "low");
		await until(() => events().some((e) => e.event === "model_changed"));
		const switched = events().filter((e) => e.event === "model_changed");
		assert.deepEqual(switched, [{ event: "model_changed", model: "hp/glm-b", thinking: "low" }]);
		await msg("msg_3", [{ type: "text", text: "piggery: model switch", synthetic: true, ignored: true }], { providerID: "hp", modelID: "kimi" }, "");
		await until(() => events().filter((e) => e.event === "model_changed").at(-1).model === "hp/kimi");
		// The same message as events: a record only of what a person or piggery said, and the tool turn played above is in.
		const ev = (type, properties) => hooks.event({ event: { type, properties } });
		await ev("message.updated", { sessionID: SID, info: { id: "msg_3", role: "user", sessionID: SID } });
		await ev("message.part.updated", { sessionID: SID, part: { id: "prt_3", type: "text", messageID: "msg_3", sessionID: SID, text: "piggery: model switch", synthetic: true, ignored: true } });
		const recs = readFileSync(join(records, SID, "records.jsonl"), "utf8");
		assert.doesNotMatch(recs, /model switch/);
		assert.match(recs, /"toolName":"bash"/);
	});

	await t.test("a permission question tells the daemon the session waits for a person", async () => {
		await play(hooks, lines(T3, 152, 153));
		await until(() => events().some((e) => e.event === "permission_wait"));
	});

	await t.test("a deleted session tells the daemon it is gone", async () => {
		await hooks.event({ event: { type: "session.deleted", properties: { info: { id: SID } } } });
		await until(() => events().some((e) => e.event === "session_end"));
	});

	await t.test("it never writes to stderr and keeps no record of a session it did not join", () => {
		assert.deepEqual(logs, []);
		assert.equal(existsSync(join(records, CHILD)), false);
		assert.equal(existsSync(join(records, "ses_kid")), false);
	});
});
