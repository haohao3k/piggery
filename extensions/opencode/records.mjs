// opencode's events as piggery's standard records (the pi-rpc shapes `piggery tail` and top read:
// message_end with text or usage, tool_execution_start/end, turn_end per model call, agent_end). Pure:
// the caller passes the events and a write. A TUI session's records go to a file it reports as its
// transcript (format "driver"), since opencode keeps its sessions in a database piggery does not read.
//
// The same records, byte for byte, as the Go driver writes for a worker from the same events
// (internal/driver/local/opencode.go, `standard`), so top and tail read both alike: keys in
// alphabetical order (Go's map order), U+2028 and U+2029 escaped as Go does (and, in an error record only,
// `<`, `>`, `&`: that one goes through Go's json.Marshal), a text cut at 20000 characters (a tool's output
// at 4000) with "…". Text and tool parts are written when they end, usage at each step, agent_end when the
// session goes idle; deltas and the startup events are not records. `write` gets each record as its line.

const clip = (s, n) => {
	const r = [...s];
	return r.length > n ? r.slice(0, n).join("") + "…" : s;
};

const escape = (re) => (s) => s.replace(re, (c) => "\\u" + c.charCodeAt(0).toString(16).padStart(4, "0"));
const line = (record) => escape(/[\u2028\u2029]/g)(JSON.stringify(record));
const htmlLine = (record) => escape(/[<>&\u2028\u2029]/g)(JSON.stringify(record));

const textRecord = (role, text) => ({ message: { content: [{ text: clip(text, 20000), type: "text" }], role }, type: "message_end" });
const errorLine = (msg) => htmlLine({ message: { errorMessage: msg, role: "assistant", stopReason: "error" }, type: "message_end" });

export class Records {
	/** @param {(line: string) => void} write */
	constructor(write) {
		this.out = write;
		this.roles = new Map(); // message id -> role
		this.seen = new Set(); // what was written already, by kind and part id
		this.busy = false;
	}

	/** One opencode event ({type, properties}) of this session. */
	event(ev) {
		const p = ev.properties ?? {};
		switch (ev.type) {
			case "message.updated":
				if (p.info?.id) this.roles.set(p.info.id, p.info.role);
				return;
			case "message.part.updated":
				return this.part(p.part ?? {});
			case "session.status":
				if (p.status?.type === "busy") this.busy = true;
				else if (p.status?.type === "idle" && this.busy) {
					this.busy = false;
					this.write({ type: "agent_end" });
				}
				return;
			case "session.error": {
				let msg = p.error?.data?.message ?? "";
				if (msg.includes("\n    at ")) return; // the second event of an error carries a stack trace
				if (p.error?.name === "MessageAbortedError") msg = "aborted";
				return this.out(errorLine(msg || p.error?.name || ""));
			}
			case "permission.asked":
				return this.out(errorLine("waiting for a permission: " + (p.permission ?? "") + " " + (p.patterns ?? []).join(" ")));
		}
	}

	write(record) {
		this.out(line(record));
	}

	once(key) {
		if (this.seen.has(key)) return false;
		this.seen.add(key);
		return true;
	}

	part(pt) {
		switch (pt.type) {
			case "text": {
				const role = this.roles.get(pt.messageID);
				const user = role === "user" && !pt.ignored && !pt.synthetic && pt.text;
				const assistant = role !== "user" && pt.time?.end > 0 && pt.text;
				if ((user || assistant) && this.once("text:" + pt.id)) this.write(textRecord(user ? "user" : "assistant", pt.text));
				return;
			}
			case "tool": {
				const st = pt.state ?? {};
				if (st.status === "running" && this.once("start:" + pt.id)) this.write({ args: st.input ?? {}, toolName: pt.tool, type: "tool_execution_start" });
				else if ((st.status === "completed" || st.status === "error") && this.once("end:" + pt.id)) {
					const isError = st.status === "error";
					this.write({ isError, result: { content: [{ text: clip((isError ? st.error : st.output) ?? "", 4000), type: "text" }] }, toolName: pt.tool, type: "tool_execution_end" });
				}
				return;
			}
			case "step-finish":
				if (this.once("step:" + pt.id)) {
					const t = pt.tokens ?? {};
					this.write({ message: { content: [], role: "assistant", usage: { cacheRead: t.cache?.read ?? 0, cacheWrite: t.cache?.write ?? 0, input: t.input ?? 0, output: t.output ?? 0, totalTokens: t.total ?? 0 } }, type: "message_end" });
					this.write({ type: "turn_end" });
				}
		}
	}
}
