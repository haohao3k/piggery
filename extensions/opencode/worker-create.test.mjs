// A new worker's plugin: the session is the one the driver made for it, which says so in its metadata.
import assert from "node:assert/strict";
import test from "node:test";
import { fakeDaemon, tempHome, until } from "../dsh/fake.mjs";

test("a new worker takes the root session whose metadata names its participant, however it learns of it", async (t) => {
	const home = tempHome();
	for (const k of Object.keys(process.env)) if (k.startsWith("PIGGERY_")) delete process.env[k];
	Object.assign(process.env, { PIGGERY_ID: "w1", PIGGERY_TOKEN: "tw", PIGGERY_RUN_ID: "rw" });
	const daemon = await fakeDaemon(home, (f) => (f.verb === "identify" ? { team_id: "T", name: "dev", role: "dev", tools: ["send"], role_card: "", run_id: f.args.run_id, protocol_version: 1 } : {}));
	t.after(() => daemon.close());
	const known = { ses_foreign: { id: "ses_foreign", directory: "/w", metadata: { piggery_participant: "w9" } }, ses_late: { id: "ses_late", directory: "/w" } }; // what client.session.get knows
	const client = { session: { get: async ({ path }) => ({ data: known[path.id] }), promptAsync: async () => {} }, app: { log: async () => {} } };
	const { default: plugin } = await import("./index.mjs");
	const hooks = await plugin.server({ client, directory: "/w" }, undefined);
	t.after(() => hooks.dispose());
	const ev = (type, properties) => hooks.event({ event: { type, properties } });
	const joined = () => daemon.calls.filter((c) => c.verb === "identify").map((c) => c.args.harness_ref);

	// Plugin() alone binds nothing: there is no session to wake.
	assert.deepEqual(daemon.calls, []);
	await ev("session.created", { sessionID: "ses_plain", info: { id: "ses_plain", directory: "/w" } });
	await ev("session.created", { sessionID: "ses_foreign", info: known.ses_foreign });
	assert.deepEqual(joined(), []);
	await ev("session.created", { sessionID: "ses_made", info: { id: "ses_made", directory: "/w", metadata: { piggery_participant: "w1" } } });
	await until(() => joined().length === 1);
	assert.deepEqual(joined(), ["ses_made"]);
	// An event of a session the plugin was not told of is looked up (client.session.get): the metadata decides.
	await ev("session.status", { sessionID: "ses_late", status: { type: "busy" } });
	await assert.rejects(hooks.tool.piggery_send.execute({ to: "x", body: "y" }, { sessionID: "ses_late" }), /not a piggery participant/);
	assert.deepEqual(joined(), ["ses_made"]);
});
