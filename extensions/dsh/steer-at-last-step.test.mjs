import assert from "node:assert/strict";
import test from "node:test";
import { Turns } from "../pi/adapter.mjs";
import { Bridge } from "./bridge.mjs";

function setup() {
	const calls = [];
	const inbox = [];
	const turns = new Turns({});
	turns.io = {
		event: async (a) => {
			calls.push(a.event + (a.outcome ? ":" + a.outcome : ""));
			return a.event === "tool_boundary" ? { text: "MAIL" } : {};
		},
		presence: async (e) => calls.push("presence:" + e),
		deliver: (text) => inbox.push(text),
		reads: true,
		newKey: () => "k",
		onError: (err) => {
			throw err;
		},
	};
	const bridge = new Bridge(turns, () => inbox.splice(0));
	return { calls, inbox, bridge };
}

test("dsh: mail steered at the last step/end is not acked by turn-stopping before a step claims it", async () => {
	const { calls, inbox, bridge } = setup();
	bridge.event({ type: "turn/start" });
	bridge.event({ type: "step/end" });
	await bridge.turnStopping();
	assert.deepEqual(inbox, ["MAIL"], "steered in, not yet claimed by a step");
	assert.ok(!calls.includes("turn_end:ok"), `turn_end ok while the mail is still in dsh's inbox: ${calls}`);
	bridge.event({ type: "turn/end", data: { reason: { kind: "aborted" } } });
	await bridge.drain();
	assert.deepEqual(inbox, [], "dropMail removed the unclaimed copy");
	assert.ok(calls.includes("turn_end:interrupted"), `the daemon must give it again: ${calls}`);
});

test("dsh: a turn that ends completed with the steered mail still unclaimed ends interrupted and drops the copy", async () => {
	const { calls, inbox, bridge } = setup();
	bridge.event({ type: "turn/start" });
	bridge.event({ type: "step/end" });
	await bridge.turnStopping();
	bridge.event({ type: "turn/end", data: { reason: { kind: "completed" } } });
	await bridge.drain();
	assert.deepEqual(inbox, [], "the copy would run twice once the daemon gives it again");
	assert.ok(calls.includes("turn_end:interrupted") && !calls.includes("turn_end:ok"), `${calls}`);
});
