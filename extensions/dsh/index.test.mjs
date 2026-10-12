// The plugin's interactive side (a `dsh web` session): the real index.mjs against a fake daemon on a
// unix socket and a fake dsh context. Worker mode is worker.test.mjs (its own process: the plugin keeps
// per-process state).
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { events } from "./replay.mjs";
import { fakeAgent, fakeCtx, fakeDaemon, tempHome, until } from "./fake.mjs";

test("a dsh web agent joins piggery: tools, role card, mail in, aborts, records", async (t) => {
	const home = tempHome();
	for (const k of Object.keys(process.env)) if (k.startsWith("PIGGERY_")) delete process.env[k];
	let mail = "";
	const daemon = await fakeDaemon(home, (f) => {
		switch (f.verb) {
			case "join.auto":
				return { id: "p1", token: "t1", run_id: "r1" };
			case "identify":
				return { team_id: "T", name: "lead", role: "peer", tools: ["send", "inbox", "who"], role_card: "team card", protocol_version: 1, run_id: f.args.run_id };
			case "harness.event": // the daemon hands mail at a turn start or boundary, and blocks an end that has some
			{
				if (!mail || !["turn_start", "tool_boundary", "turn_end"].includes(f.args.event) || (f.args.event === "turn_end" && f.args.outcome !== "ok")) return {};
				const text = mail;
				mail = ""; // handed over once
				return { text, block: f.args.event === "turn_end" };
			}
			case "send":
				return { seq: 7 };
			default:
				return {};
		}
	});
	t.after(() => daemon.close());

	const agent = fakeAgent("s1");
	const sub = fakeAgent("s2", { origin: "subagent", child: true });
	const live = [];
	const ctx = fakeCtx({ agents: live });
	const { apply } = await import("./index.mjs");
	const sessions = join(home, ".piggery", "sessions", "dsh"); // what setup writes into the row
	apply(ctx, { sessions });
	live.push(agent, sub);
	await ctx.emit("agent/created", { agent, source: "startup" });
	await ctx.emit("agent/created", { agent: sub, source: "startup" }); // a subagent is nobody's participant
	t.after(() => ctx.dispose());
	const as = (verb) => daemon.calls.filter((c) => c.verb === verb);

	await t.test("it joins by its session id, reports model, capabilities and its records file", () => {
		assert.equal(as("join.auto").length, 1);
		const join1 = as("join.auto")[0].args;
		assert.deepEqual([join1.harness, join1.mode, join1.harness_ref, join1.cwd, join1.tool_prefix], ["dsh", "interactive", "s1", "/w", "piggery_"]);
		assert.equal(join1.transcript.format, "driver");
		assert.equal(join1.transcript.path, join(sessions, "s1", "records.jsonl"));
		const id = as("identify")[0].args;
		assert.deepEqual([id.model, id.thinking, id.new_run, id.protocol_version], ["hp/glm-5.3-flash", "", false, 1]);
		assert.deepEqual(id.capabilities, ["abort", "wake", "steer", "system_prompt"]);
	});

	await t.test("the role's tools (prefixed, tools.json's) and its role card", () => {
		const file = JSON.parse(readFileSync(new URL("../pi/tools.json", import.meta.url), "utf8")).tools;
		assert.deepEqual(Object.keys(agent.tools).sort(), ["send", "inbox", "who"].map((n) => "piggery_" + n).sort()); // the role has no agent tool
		for (const f of file.filter((f) => f.name !== "agent")) {
			const d = agent.tools["piggery_" + f.name];
			assert.deepEqual(d.parameters, f.parameters);
			assert.doesNotMatch(d.description, /\{tool:/);
		}
		assert.equal(agent.sections["piggery-role"].text(), "team card");
	});

	await t.test("a tool call goes to the daemon as the agent", async () => {
		const out = await agent.tools.piggery_send.execute({ to: "boss", body: "hi" });
		assert.equal(out, "sent #7");
		assert.deepEqual(as("send")[0].args, { to: "boss", body: "hi" });
		assert.equal(as("send")[0].as, "p1");
	});

	await t.test("a wake opens a turn with the mail; while running the mail is steered in", async () => {
		mail = "[piggery] 1 new message";
		daemon.push("wake");
		await until(() => agent.followed.length === 1);
		assert.deepEqual([agent.followed[0].role, agent.followed[0].source, agent.followed[0].content[0].text], ["user", { kind: "piggery-mail" }, "[piggery] 1 new message"]);
		agent.status = "running";
		const events = () => as("harness.event").map((c) => c.args.event);
		ctx.session(agent, { type: "turn/start", data: { turn: 1 } }); // the run the wake opened
		await until(() => events().filter((e) => e === "turn_start").length === 3); // one at the join, the wake, this start
		mail = "[piggery] 2 new messages";
		ctx.session(agent, { type: "step/end", data: { turn: 1, step: 1 } });
		await until(() => agent.steered.length === 1); // mail at the step boundary
		assert.equal(agent.steered[0].content[0].text, "[piggery] 2 new messages");
	});

	await t.test("mail at agent/turn-stopping is steered in before the turn ends", async () => {
		mail = "[piggery] 3 new messages";
		const before = agent.steered.length;
		ctx.session(agent, { type: "step/start", data: { turn: 1, step: 2 } }); // dsh runs the step that claims the mail steered at the boundary, then stops
		const stopping = ctx.handlers["agent/turn-stopping"][0].fn({ agent, turn: 1 });
		await stopping;
		assert.equal(agent.steered.length, before + 1);
		assert.equal(as("harness.event").findLast((c) => c.args.event === "turn_end").args.outcome, "ok");
	});

	await t.test("abort cancels the turn and keeps the inbox; the turn ends interrupted and drops piggery's own unread mail", async () => {
		daemon.push("abort");
		await until(() => agent.cancels.length === 1);
		assert.deepEqual(agent.cancels[0], { cause: { kind: "hook", reason: "piggery abort" }, opts: { keepInbox: true } });
		agent.inbox.nextStep = [{ id: "m1", source: { kind: "piggery-mail" } }, { id: "m2", source: { kind: "user" } }];
		ctx.session(agent, { type: "turn/end", data: { turn: 1, reason: { kind: "aborted", reason: { kind: "hook" } } } });
		await until(() => agent.inbox.nextStep.length === 1);
		assert.deepEqual(agent.inbox.nextStep.map((m) => m.id), ["m2"]); // the person's own queued message stays
	});

	await t.test("the model in use follows the request configuration", async () => {
		const [{ fn }] = ctx.handlers["agent/request"];
		const cfg = await fn({ agent }, async () => ({ provider: "hp", model: "kimi-k3", reasoningEffort: "high" }));
		assert.equal(cfg.model, "kimi-k3");
		await until(() => as("harness.event").some((c) => c.args.event === "model_changed"));
		assert.deepEqual(as("harness.event").findLast((c) => c.args.event === "model_changed").args, { event: "model_changed", model: "hp/kimi-k3", thinking: "high" });
	});

	await t.test("what the agent said and did is in its records file (the transcript top and tail read)", async () => {
		for (const ev of events("tool")) ctx.session(agent, ev);
		const file = join(sessions, "s1", "records.jsonl");
		await until(() => readFileSync(file, "utf8").includes("agent_end"));
		const recs = readFileSync(file, "utf8").trim().split("\n").map((l) => JSON.parse(l));
		assert.deepEqual(recs.filter((r) => r.type === "tool_execution_start").map((r) => r.toolName), ["probe_send"]);
		assert.ok(recs.some((r) => r.type === "message_end" && r.message.role === "assistant" && r.message.usage.totalTokens > 0));
	});

	await t.test("a subagent never joined", () => {
		assert.deepEqual([...new Set(as("join.auto").map((c) => c.args.harness_ref))], ["s1"]);
	});

	await t.test("a disposed agent tells the daemon it is gone", async () => {
		await ctx.emit("agent/disposed", { agent });
		await until(() => as("harness.event").some((c) => c.args.event === "session_end"));
	});
});
