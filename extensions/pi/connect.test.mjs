// Tool calls made while the pi session is still joining wait for that join. Seen live: a model
// called two piggery tools at once on a fresh session; each restarted the client, the daemon refused
// the second join.auto of the same session ("participant is live"), and the session stayed inert.
// Its own file: the extension keeps per-process state.
import assert from "node:assert/strict";
import { mkdtempSync } from "node:fs";
import { register } from "node:module";
import net from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fakeDaemonPath, setHome } from "../testutil/daemon.mjs";

const piAi = "export const Type = new Proxy({}, { get: (_, k) => (k === 'Unsafe' ? (s) => s : (...a) => ({ a })) });";
register(
	"data:text/javascript," +
		encodeURIComponent(`export async function resolve(s, c, next) {
			if (s === "@earendil-works/pi-ai") return { url: "data:text/javascript," + ${JSON.stringify(encodeURIComponent(piAi))}, shortCircuit: true };
			return next(s, c);
		}`),
);

test("tool calls made while the session joins wait for that join", async (t) => {
	setHome(mkdtempSync(join(tmpdir(), "pgconn")));
	delete process.env.PIGGERY_ID;
	delete process.env.PIGGERY_TOKEN;
	delete process.env.PIGGERY_DISABLED;

	// Like the daemon: the first join.auto of a session places it (answered late, so the client is
	// not ready yet); any later one for the same session is refused while that participant lives.
	const joins = [];
	const srv = net.createServer((s) => {
		s.on("error", () => {});
		let buf = "";
		s.on("data", (d) => {
			buf += d;
			for (let i; (i = buf.indexOf("\n")) >= 0; ) {
				const f = JSON.parse(buf.slice(0, i));
				buf = buf.slice(i + 1);
				const reply = (r) => !s.destroyed && s.write(JSON.stringify({ id: f.id, ...r }) + "\n");
				if (f.verb === "join.auto") {
					joins.push(f.args.harness_ref);
					if (joins.length > 1) reply({ error: { code: "invalid", message: "participant is live" } });
					else setTimeout(() => reply({ result: { id: "p1", token: "t1", run_id: "r1" } }), 200);
				} else if (f.verb === "identify") {
					reply({ result: { team_id: "", name: "pi-1", tools: ["send", "inbox", "who", "agent"], role_card: "" } });
				} else reply({ result: [] });
			}
		});
	});
	await new Promise((r) => srv.listen(fakeDaemonPath(process.env.HOME), r));
	t.after(() => srv.close());

	const tools = {};
	const handlers = {};
	let active = [];
	const pi = new Proxy(
		{
			registerTool: (d) => (tools[d.name] = d),
			on: (e, fn) => (handlers[e] = fn),
			getActiveTools: () => active,
			setActiveTools: (a) => (active = a),
			getThinkingLevel: () => "high",
		},
		{ get: (o, k) => o[k] ?? (() => {}) },
	);
	const { default: piggery } = await import("./index.ts");
	piggery(pi);
	const ctx = { mode: "rpc", hasUI: false, cwd: process.env.HOME, isIdle: () => true, model: { provider: "HP", id: "glm-5.3-flash" }, sessionManager: { getSessionId: () => "sess-1", getSessionFile: () => undefined } };
	handlers.session_start({}, ctx);
	for (const end = Date.now() + 5000; joins.length === 0; ) {
		if (Date.now() > end) throw new Error("no join.auto");
		await new Promise((r) => setTimeout(r, 10));
	}

	const results = await Promise.allSettled([tools.piggery_inbox.execute("c1", {}), tools.piggery_inbox.execute("c2", {})]);
	for (const r of results) assert.equal(r.status, "fulfilled", r.reason?.message);
	assert.equal(joins.length, 1);
	handlers.session_shutdown?.({ reason: "quit" });
});
