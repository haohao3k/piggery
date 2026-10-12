// dsh's turn events as the standard adapter events (pi's Turns, extensions/pi/adapter.mjs).
// Pure: no dsh, no socket.
//
// A dsh turn is turn/start ... turn/end (session events); a step (one model call and its tools)
// is step/start ... step/end. pi's awaited "before settle" is dsh's agent/turn-stopping: it runs
// only when the turn ends by itself, and mail steered in there makes the same turn run another
// step, so the turn is acked there (outcome ok) exactly as pi's before_settle acks it, once no
// steered mail is unread: mail steered at the last step/end is read by the next step (step/start),
// whose own turn-stopping acks it.
// turn/end then settles the run; how the turn ended decides the rest:
//   error                          failed (the turn_end the daemon gets, no ack)
//   aborted, blocked, interrupted  the run settles with no end of its own: Turns ends the turn
//                                  interrupted (no ack, no wake), so its mail comes again.

export class Bridge {
	/**
	 * @param {import("../pi/adapter.mjs").Turns} turns
	 * @param {() => void} dropMail removes the mail steered in and not yet claimed: the daemon
	 *   gives an unacked turn's mail again, so the copy left in dsh's inbox would run twice
	 */
	constructor(turns, dropMail) {
		this.turns = turns;
		this.dropMail = dropMail;
		this.q = Promise.resolve(); // events in order: an error end waits for its daemon call
	}

	/** One dsh session event ({type, data}) of this agent, in order. */
	event(ev) {
		this.q = this.q.then(() => this.handle(ev));
	}

	async handle(ev) {
		switch (ev.type) {
			case "turn/start":
				this.turns.agentStart();
				return;
			case "step/start": // a model call starts: mail steered before it is in it
				this.turns.modelTurn();
				return;
			case "step/end":
				this.turns.toolBoundary();
				return;
			case "turn/end": {
				const kind = ev.data?.reason?.kind;
				if (kind === "error") await this.turns.beforeSettle("error");
				// Mail still unread ends the turn interrupted (Turns.settled), so it comes again: the copy
				// left in dsh's inbox is dropped whatever the reason.
				else if ((kind !== "completed" && kind !== "max-tokens") || this.turns.unread) this.dropMail();
				this.turns.settled();
				return;
			}
		}
	}

	/** agent/turn-stopping, awaited by dsh: mail the daemon hands back is steered in by Turns' deliver. */
	async turnStopping() {
		await this.q;
		await this.turns.beforeSettle("completed");
	}

	/** Resolves when every event so far has been handled and sent (tests, shutdown). */
	async drain() {
		await this.q;
		await this.turns.drain();
	}
}
