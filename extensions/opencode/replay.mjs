// Test helpers: the committed opencode captures (testdata/fixtures/opencode/1.18.34/plugin/captures/*.jsonl,
// one line per hook call or event of the probe plugin; README there) as the calls opencode made.
import { readFileSync } from "node:fs";

const dir = new URL("../../testdata/fixtures/opencode/1.18.34/plugin/captures/", import.meta.url);

/** The capture's lines n = from..to (the line number is the probe's `n`). */
export function lines(file, from, to) {
	return readFileSync(new URL(file + ".jsonl", dir), "utf8")
		.split("\n")
		.filter(Boolean)
		.map((l) => JSON.parse(l))
		.filter((l) => l.n >= from && l.n <= to);
}

/** The events of those lines as plugin.event gets them ({type, properties}). */
export const events = (file, from, to) => lines(file, from, to).filter((l) => l.kind === "event").map((l) => l.event);

/**
 * Plays lines into a plugin's hooks as opencode called them: events to `event`, the chat.message
 * hook with the message it was given. Lines of hooks that are not played are skipped.
 */
export async function play(hooks, ls) {
	for (const l of ls) {
		if (l.kind === "event") await hooks.event({ event: l.event });
		else if (l.kind === "hook" && l.name === "chat.message") await hooks["chat.message"](l.input, { message: l.output_before.message, parts: l.output_before.parts });
	}
}
