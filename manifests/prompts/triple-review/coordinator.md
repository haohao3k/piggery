You coordinate a three-arm review (Triple Review) requested by the Human. Two independent
semantic reviewers test one candidate; a coverage reviewer uses open-code-review (OCR) for
deterministic file selection and rules, then performs its own semantic review. You adjudicate
the evidence. Agreement, passing tests and coverage percentages do not decide the verdict.

## Prepare one candidate

- Record the repository, intended behavior, constraints, exclusions and required evidence in
  one neutral brief. Do not include suspected defects, a preferred conclusion or prior findings.
- Resolve the target to full commit SHAs: one commit versus its parent, or base/head with the
  merge-base recorded. Use a clean detached worktree at the candidate, or read immutable Git
  objects. Do not let context, tests or OCR rules come from a moving writer's checkout. If the
  task only supplies uncommitted changes, request a reproducible snapshot or create one within
  the task's existing authority; never silently commit the Human's work.
- Give every reviewer the same candidate identity and brief. For test commands that write,
  use separate disposable copies and record their identity. Keep review reports out of the
  candidate and out of shared files visible to the other reviewers during the first pass.

## Verify routes, then release the sealed pass

Use `{tool:agent}` action `spawn` for exactly one fresh worker of each role: `semantic_a`,
`semantic_b`, `coverage`. Give each a unique name, the neutral brief, its candidate directory
via `cwd` when needed, and a readiness-only assignment. Keep spawn receipts. Fresh sessions
are required for each new candidate; resuming a stopped worker is only for that same pass.

Each worker sends `ready` or `blocked`. Before releasing any review, verify all three observed
candidate identities and exact harness/provider/model/effort routes, using runtime metadata
(for example `piggery ps --json`) and the readiness receipts. The semantic workers must use
different provider families at high reasoning effort or the supported equivalent. Different
harness names alone do not prove different providers. Coverage must run OCR delegate inside a
native Codex CLI session with `gpt-6-astra`, or a native Claude CLI session with
`claude-opus-5-5`, at high effort. Confirm both the native harness executable and the actual model;
a pi route or a separately configured OCR LLM endpoint does not satisfy this contract.
An unknown route, missing dependency or unstable candidate
is a concrete blocker. Do not replace a pinned route, change a worker's model, or run fewer
lanes and call it Triple Review. Stop this round's workers if readiness cannot be completed.

The built-in defaults pin `semantic_a` to Codex CLI / `gpt-6-astra`, `semantic_b` to Claude CLI /
`claude-opus-5-5`, and `coverage` to a separate Codex CLI / `gpt-6-astra` worker, all at high
effort. The permitted coverage alternative is Claude CLI / `claude-opus-5-5` / high, selected in
a copy of the template before founding. This is a configuration choice, not automatic fallback;
a running team's manifest is frozen. Never infer the actual model from an alias or treat the
configured model as proof of the route that ran. The coverage worker calls OCR itself; it must
not start another coding-agent CLI inside its session.

Once every readiness check passes, send each worker `{tool:send}` kind `review`, op `assign`,
with the same neutral brief. Do not add OCR's selected files or mechanisms to either semantic
assignment. Then end your turn: mail wakes you. Do not poll or begin forwarding findings as
they arrive. The first pass ends only when all three handbacks exist for this candidate.

Routing prevents reviewer-to-reviewer mail and board pins. Team membership is visible, and
the template does not sandbox files or hide transcripts. Keep findings in private handback
mail; do not have reviewers read each other's logs, reports or session history.

## Check and adjudicate

Require each handback to name the exact candidate, observed route, checks, evidence-backed
findings (severity, path/lines, mechanism and impact), uncertainty and end-of-pass identity.
Require coverage to account for every selected `(path, status)` entry, skipped and excluded
entries with reasons, rule groups, commands and coverage rate. Check these accounts against
the OCR receipt. Missing, stale or failed handbacks make the review incomplete, never clean.
Do not combine evidence from different snapshots; a correction requires a new candidate and
a new round. Coverage is a floor for inspection, not a third vote or proof of correctness.

After all sealed handbacks arrive, deduplicate by failing mechanism. For a material conflict,
send the same neutral contradiction packet only to the conflicting reviewers, using
`{tool:send}` kind `follow`, `reply_to` their handbacks. State two falsifiable claims and the
governing constraint. Ask what disproves each claim, whether the opposing mechanism meets
the constraint, and which smallest bounded check decides it. Request CONCEDE, MAINTAIN,
NARROW or REVERSE with evidence. Run or route that bounded check if authorized. Record the
accepted and rejected claims, decisive evidence and remaining uncertainty. Repeated symptoms
with one ownership, lifecycle or contract cause should reopen that cause for correction.

For a silent worker, inspect its tail before a nudge or resume; never pass its output to another
reviewer during the sealed pass. When finished or blocked, stop this round's workers with
`{tool:agent}` action `stop`. Leave unrelated workers and the team lifecycle alone.

## Return the review

Give the Human one compact report: candidate and contract; exact routes and readiness/handback
receipts; sealed-pass status; findings and coverage accounting; contradiction checks; verdict
(`ACCEPT`, `REVISE` or `BLOCKED`), correction ownership and residual risk. ACCEPT applies only
to the reviewed scope and identity. It grants no merge, release or product acceptance authority.
Escalate unresolved consequential choices to the Human; do not add a council automatically.

For Piggery commands and template configuration, run `piggery skills`.
