// opencode's session events as the standard adapter events (pi's Turns, extensions/pi/adapter.mjs).
// Pure: no opencode, no socket. Evidence: testdata/fixtures/opencode/1.18.34/plugin (README).
//
// A run is what opencode's loop does until the session is idle: the first `busy` opens it, one
// `session.idle` ends it, however many prompts it answered (a prompt sent while busy is read at the
// loop's next step: a steer). `busy` repeats every step, so only the first one after an idle counts.
//   assistant message finished "tool-calls"  the tools are done, the loop reads the session again:
//                                            mail can be steered in (a user message now)
//   session.idle                             the run ends: the outcome is the last assistant message's
//   abort                                    session.error MessageAbortedError, then idle, then the
//                                            assistant message with that error, then idle again
//                                            (~200 ms later): the run ends once, interrupted, at the
//                                            message or the second idle, never at the first idle
//
// Mail the daemon hands back at the end of a run (Turns.endRun: block) is a new prompt to an idle
// session, so there is no window in which opencode could miss it. Mail steered in at a boundary is
// checked at the idle: a user message newer than every assistant message's parent was never read
// (it arrived after the loop's last look), so the run is not acked: the session gets a nudge and
// the same turn goes on.

const ABORTED = "MessageAbortedError";
const ABORT_WAIT_MS = 3000; // an abort whose message error and second idle never come

export const NUDGE = "[piggery] A piggery message reached you as your turn ended. Read it above and continue.";

export class Bridge {
	/**
	 * @param {import("../pi/adapter.mjs").Turns} turns
	 * @param {{nudge: () => void, abortWait?: number}} io nudge asks the session for one more run; abortWait
	 *   is how long an abort may wait for its message and second idle (ms)
	 */
	constructor(turns, io) {
		this.turns = turns;
		this.io = io;
		this.q = Promise.resolve(); // events in order: an idle waits for its daemon calls
		this.mail = new Set(); // ids of user messages this plugin delivered and no run has answered
		this.reset();
	}

	reset() {
		this.parent = ""; // the newest user message an assistant message of this run answered
		this.last = null; // the run's latest assistant message {id, finish, error}
		this.boundary = new Set(); // assistant messages whose tool-calls end was told
		this.aborting = false; // session.error MessageAbortedError seen
		this.abortSeen = false; // the aborted assistant message seen
		this.deferred = false; // the first idle of an abort seen, its end waits for the message or the second idle
		clearTimeout(this.timer);
	}

	/** A user message this plugin delivered (piggery mail), by id, from the chat.message hook. */
	delivered(id) {
		this.mail.add(id);
	}

	/** One opencode event ({type, properties}) of this session, in order. */
	event(ev) {
		this.q = this.q.then(() => this.handle(ev)).catch((err) => this.turns.io.onError(err));
		return this.q;
	}

	async handle(ev) {
		const p = ev.properties ?? {};
		switch (ev.type) {
			case "session.status":
				if (p.status?.type === "busy" && !this.turns.running) {
					this.reset();
					this.turns.agentStart();
				}
				return;
			case "message.updated":
				return this.message(p.info ?? {});
			case "session.error":
				if (p.error?.name === ABORTED) this.aborting = true;
				return;
			case "session.idle":
				return this.idle();
			case "permission.asked":
				this.turns.uiPrompt(true);
				return;
			case "permission.replied":
				this.turns.uiPrompt(false);
				return;
		}
	}

	async message(info) {
		if (info.role !== "assistant") return;
		if (info.parentID && info.parentID > this.parent) this.parent = info.parentID;
		this.last = { id: info.id, finish: info.finish, error: info.error };
		if (info.finish === "tool-calls" && !this.boundary.has(info.id)) {
			this.boundary.add(info.id);
			this.turns.toolBoundary();
		}
		if (info.error?.name === ABORTED) {
			this.aborting = true;
			this.abortSeen = true;
			if (this.deferred) await this.endAborted();
		}
	}

	async idle() {
		if (!this.turns.running) return; // an abort of an idle session, or the second idle of an abort
		if (this.aborting) {
			if (!this.abortSeen && !this.deferred) {
				// The first idle of an abort: the tool and the message are not finished yet.
				this.deferred = true;
				this.timer = setTimeout(() => (this.q = this.q.then(() => this.endAborted()).catch((err) => this.turns.io.onError(err))), this.io.abortWait ?? ABORT_WAIT_MS);
				this.timer.unref?.();
				return;
			}
			return this.endAborted();
		}
		if ([...this.mail].some((id) => id > this.parent)) {
			// Delivered, never read: ending the run would ack it. One more run reads it, in this turn.
			this.io.nudge();
			return;
		}
		const error = this.last?.error;
		this.mail.clear(); // before the end: mail the daemon hands back is a new user message that comes after
		await this.turns.endRun(error ? (error.name === ABORTED ? "aborted" : "error") : "completed");
	}

	async endAborted() {
		if (!this.turns.running) return;
		this.reset();
		this.mail.clear(); // unread, so unacked: the daemon gives it again
		await this.turns.endRun("aborted");
	}

	/** Resolves when every event so far has been handled and sent (tests, shutdown). */
	async drain() {
		await this.q;
		await this.turns.drain();
	}
}
