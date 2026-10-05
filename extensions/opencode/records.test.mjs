// The records a TUI session keeps are the Go driver's, line by line as JSON values (key order and escaping are not
// part of the contract, the CLI reads JSON; in fact they are equal byte for byte): every golden file of the driver
// (testdata/fixtures/opencode/1.18.34/records-golden/<capture>.jsonl) against the serve capture it was made from
// (serve/<capture>.sse.jsonl), the events of the session of its first message.updated (the driver's own rule:
// 03 made another session first, from a create whose id opencode ignored).
import assert from "node:assert/strict";
import { readdirSync, readFileSync } from "node:fs";
import test from "node:test";
import { Records } from "./records.mjs";

const fixtures = new URL("../../testdata/fixtures/opencode/1.18.34/", import.meta.url);
const goldens = readdirSync(new URL("records-golden/", fixtures)).filter((f) => f.endsWith(".jsonl"));

test("there are goldens to compare with", () => assert.ok(goldens.length >= 4, `records-golden has ${goldens.length} files`));

for (const golden of goldens) {
	const capture = golden.replace(/\.jsonl$/, "");
	test(`records of ${capture} equal the driver's`, () => {
		const events = readFileSync(new URL(`serve/${capture}.sse.jsonl`, fixtures), "utf8")
			.split("\n")
			.filter(Boolean)
			.map((l) => JSON.parse(l).data)
			.filter(Boolean);
		const sid = events.find((e) => e.type === "message.updated")?.properties.sessionID;
		assert.ok(sid, "the capture has a message");
		const out = [];
		const records = new Records((l) => out.push(l));
		for (const ev of events) {
			const p = ev.properties ?? {};
			const s = p.sessionID ?? p.info?.sessionID ?? p.part?.sessionID;
			if (!s || s === sid) records.event(ev);
		}
		const want = readFileSync(new URL(`records-golden/${golden}`, fixtures), "utf8").split("\n").filter(Boolean);
		assert.deepEqual(out.map((l) => JSON.parse(l)), want.map((l) => JSON.parse(l)));
	});
}
