// The plugin as a resumed worker's (`opencode serve`, PIGGERY_OPENCODE_SESSION in its environment): its
// own process, since the plugin keeps per-process state and reads the environment when it loads.
import assert from "node:assert/strict";
import { existsSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { fakeDaemon, tempHome, until } from "../dsh/fake.mjs";

test("a resumed worker joins at once as the participant the driver named, takes the session it was given and no other", async (t) => {
	const home = tempHome();
	for (const k of Object.keys(process.env)) if (k.startsWith("PIGGERY_")) delete process.env[k];
	Object.assign(process.env, { PIGGERY_ID: "w1", PIGGERY_TOKEN: "tw", PIGGERY_RUN_ID: "rw", PIGGERY_OPENCODE_SESSION: "ses_w" });
	let mail = "[piggery] 1 new message"; // waiting for the session: nothing will ever announce it
	const daemon = await fakeDaemon(home, (f) => {
		if (f.verb === "identify") return { team_id: "T", name: "dev", role: "dev", tools: ["send"], role_card: "worker card", run_id: f.args.run_id, protocol_version: 1 };
		if (f.verb === "harness.event" && f.args.event === "turn_start" && mail) {
			const text = mail;
			mail = "";
			return { text };
		}
		return {};
	});
	t.after(() => daemon.close());
	const sdk = [];
	const client = {
		session: {
			get: async () => ({ data: { id: "ses_w", model: { variant: "default" } } }),
			promptAsync: async (a) => void sdk.push([a.path.id, a.body]),
		},
		app: { log: async () => {} },
	};
	const { default: plugin } = await import("./index.mjs");
	const hooks = await plugin.server({ client, directory: "/tmp/oc/work" }, undefined);
	t.after(() => hooks.dispose());
	const as = (verb) => daemon.calls.filter((c) => c.verb === verb);

	await t.test("no event came, and the session is woken: identified with the credentials from the environment, no join.auto", async () => {
		await until(() => sdk.length === 1);
		assert.deepEqual(sdk[0], ["ses_w", { parts: [{ type: "text", text: "[piggery] 1 new message" }] }]); // variant "default": none, and never a model
		assert.equal(as("join.auto").length, 0);
		const id = as("identify")[0];
		assert.deepEqual([id.as, id.args.run_id, id.args.mode, id.args.harness_ref, id.args.new_run], ["w1", "rw", "rpc", "ses_w", false]);
	});

	await t.test("its identity is not left for the processes it starts, which are not members", () => {
		assert.deepEqual(Object.keys(process.env).filter((k) => k.startsWith("PIGGERY_")), ["PIGGERY_DISABLED"]);
		assert.equal(process.env.PIGGERY_DISABLED, "1");
	});

	await t.test("another root session and a child are ignored; the worker writes no records (the driver does)", async () => {
		const ev = (type, properties) => hooks.event({ event: { type, properties } });
		await ev("session.created", { sessionID: "ses_other", info: { id: "ses_other", directory: "/tmp/oc/work" } });
		await ev("session.created", { sessionID: "ses_kid", info: { id: "ses_kid", parentID: "ses_w", directory: "/tmp/oc/work" } });
		await ev("session.status", { sessionID: "ses_w", status: { type: "busy" } });
		await ev("message.updated", { sessionID: "ses_w", info: { id: "msg_a", role: "user", sessionID: "ses_w" } });
		await ev("message.part.updated", { sessionID: "ses_w", part: { id: "prt_a", type: "text", messageID: "msg_a", sessionID: "ses_w", text: "hello" } });
		await assert.rejects(hooks.tool.piggery_send.execute({ to: "x", body: "y" }, { sessionID: "ses_other" }), /not a piggery participant/);
		await until(() => as("harness.event").some((c) => c.args.event === "turn_start" && !c.args.wake));
		assert.deepEqual([...new Set(as("identify").map((c) => c.args.harness_ref))], ["ses_w"]);
		assert.equal(existsSync(join(home, ".piggery", "sessions")), false);
	});
});
