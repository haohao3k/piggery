// The turn mapping (bridge.mjs onto pi's Turns) against opencode's real events: the probe's captures
// (testdata/fixtures/opencode/1.18.34/plugin/captures, README there) played as opencode sent them.
import assert from "node:assert/strict";
import test from "node:test";
import { Turns } from "../pi/adapter.mjs";
import { Bridge } from "./bridge.mjs";
import { events } from "./replay.mjs";

// A bridge over a Turns whose daemon calls are recorded; `give(call)` is the daemon's answer.
function rig(give = () => ({})) {
	const calls = [];
	const nudges = [];
	let n = 0;
	const turns = new Turns({
		event: async (a) => {
			calls.push(a.event === "turn_end" ? `turn_end ${a.outcome}` : a.event + (a.wake ? " wake" : ""));
			return give(a);
		},
		presence: async (e) => void calls.push(`presence ${e}`),
		deliver: (text) => calls.push(`deliver ${text}`),
		newKey: () => `k${++n}`,
		onError: (e) => assert.fail(e),
	});
	const bridge = new Bridge(turns, { nudge: () => nudges.push("nudge"), abortWait: 40 });
	const play = async (evs) => {
		for (const ev of evs) await bridge.event(ev);
		await bridge.drain();
	};
	return { calls, nudges, bridge, play };
}

const T1 = "tui-1-port-turns-steer";
const T2 = "tui-2-port-wake-abort";
const T3 = "tui-3-port-permission-model-sessions";

test("a plain turn: opened by the first busy, one end at the idle, acked ok", async () => {
	const { calls, play } = rig();
	await play(events(T1, 60, 171));
	assert.deepEqual(calls, ["turn_start", "turn_end ok", "presence agent_settled"]);
});

test("a turn with tool calls: the step that ended in tool-calls is a boundary, repeated busy is not a new turn", async () => {
	const { calls, play } = rig();
	await play(events(T1, 172, 284));
	assert.deepEqual(calls, ["turn_start", "tool_boundary", "turn_end ok", "presence agent_settled"]);
});

test("a prompt typed while busy is part of the same run: one turn, one end (tui-1:285-391)", async () => {
	const { calls, play } = rig();
	await play(events(T1, 285, 391));
	assert.deepEqual(calls, ["turn_start", "tool_boundary", "turn_end ok", "presence agent_settled"]);
});

test("mail the daemon hands out at a tool boundary is delivered into the running turn", async () => {
	const { calls, play } = rig((a) => (a.event === "tool_boundary" ? { text: "mail A" } : {}));
	await play(events(T1, 172, 284));
	assert.deepEqual(calls.filter((c) => c.startsWith("deliver")), ["deliver mail A"]);
});

test("abort (Esc): the run ends once, interrupted, at the aborted message, not at the first idle (tui-2:176-251)", async () => {
	const { calls, play } = rig();
	const evs = events(T2, 176, 251);
	const firstIdle = evs.findIndex((e) => e.type === "session.idle");
	await play(evs.slice(0, firstIdle + 1)); // ... session.error, status idle, session.idle
	assert.deepEqual(calls, ["turn_start"]); // the tool and the message are not finished: no end yet
	await play(evs.slice(firstIdle + 1));
	assert.deepEqual(calls, ["turn_start", "turn_end interrupted", "presence agent_settled"]); // the second idle ends nothing more
});

test("abort by the API, then an abort of the idle session: still one end, and the idle one starts nothing (tui-2:252-342)", async () => {
	const { calls, play } = rig();
	await play(events(T2, 252, 342));
	assert.deepEqual(calls, ["turn_start", "turn_end interrupted", "presence agent_settled"]);
});

test("an abort whose message error and second idle never come ends after a wait", async () => {
	const { calls, bridge } = rig();
	const evs = events(T2, 252, 342);
	const idle = evs.findIndex((e) => e.type === "session.idle");
	for (const ev of evs.slice(0, idle + 1)) await bridge.event(ev);
	assert.deepEqual(calls, ["turn_start"]);
	await new Promise((r) => setTimeout(r, 120));
	await bridge.drain();
	assert.deepEqual(calls, ["turn_start", "turn_end interrupted", "presence agent_settled"]);
});

test("the run after an abort is a new turn", async () => {
	const { calls, play } = rig();
	await play(events(T2, 252, 342));
	await play(events(T2, 343, 423));
	assert.deepEqual(calls.slice(3), ["turn_start", "turn_end ok", "presence agent_settled"]);
});

test("a permission question holds the turn: permission_wait, then the prompt ends (tui-3:60-198)", async () => {
	const { calls, play } = rig();
	await play(events(T3, 60, 198));
	assert.deepEqual(calls.filter((c) => c !== "tool_boundary"), ["turn_start", "permission_wait", "presence ui_prompt_end", "turn_end ok", "presence agent_settled"]);
});

test("mail steered in and never read is not acked: the session is nudged and the turn goes on", async () => {
	const { calls, nudges, bridge, play } = rig();
	const evs = events(T1, 60, 171);
	const idle = evs.findIndex((e) => e.type === "session.idle");
	bridge.delivered("msg_zzzz"); // a user message newer than every assistant message's parent
	await play(evs.slice(0, idle + 1));
	assert.deepEqual([calls, nudges], [["turn_start"], ["nudge"]]);
});
