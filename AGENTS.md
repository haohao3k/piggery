# Piggery fork development

## Repository ownership

- The working remote is `https://github.com/haohao3k/piggery.git`. `origin`, the push default,
  and the GitHub CLI default repository must point to this fork. Use `codex/` branches.
- `upstream` is `sting8k/piggery`, for fetching and comparison only. Never push there. Preserve
  its disabled push URL. Do not merge, publish a release, or push to another repository without
  the Human's authorization.
- Preserve unrelated work and existing templates, profiles, sessions, messages and credentials.
  A shared changing scope has one write owner; coordinate before editing another owner's files.
- Use Semble for exploratory code discovery, then read the returned locations directly. Use
  literal `rg` searches when all occurrences are needed.

## Hard constraint: a change is not done until the local runtime matches it

For every Piggery source, CLI, adapter, embedded prompt/template/skill, integration asset, or
build/install-script change:

1. Run checks appropriate to the changed behavior. Build from this checkout, including its
   current embedded assets. Use `./scripts/local-dev.sh build`; a release download or
   `go install github.com/sting8k/piggery/cmd/piggery@latest` is not a substitute.
2. Run `./scripts/local-dev.sh apply` to install the candidate, refresh already-installed
   integration assets and restart the same Piggery home. This workflow is authorized by the
   Human's local-development direction; do not ask for another approval for a routine safe apply.
3. Run `./scripts/local-dev.sh check`. Completion requires current source fingerprint, installed
   CLI identity/hash and live daemon version to match, plus successful asset refresh evidence.
   A successful compile or an edited template alone is not completion.
4. Rebuild/apply again if source or assets changed after the candidate was built. Keep the final
   check result with the handback, including source revision, version and any remaining limits.

`apply` must obtain a fresh authoritative runtime readback before replacing the executable or
restarting. Busy/starting/awaiting-permission or unknown states block activation. The exact
invoking solo session may be identified with `--caller <participant-id>`; this must never exempt
another session or a headless worker. Idle workers are stopped by Piggery's normal graceful
restart, their exits/sessions are retained, and the receipt must list them. Do not automatically
resume them or start provider calls as part of installation.

If activation is blocked, finish the source/tests/candidate and report **BLOCKED activation**
with the exact state. Do not claim the installed runtime is current, weaken the gate, or kill
busy agents to satisfy this rule. Recheck when the blocking state changes.

The stable executable is the local installation on PATH (normally `~/.local/bin/piggery`).
Keep the existing Piggery home and configuration; do not reset profiles, overwrite custom
project templates, print tokens, or copy the database into the repository. `setup --refresh`
refreshes installed assets even when their integration-version integer did not change.
Custom template conflicts must remain visible instead of being silently overwritten.

Existing long-lived harness processes may retain loaded code or instructions until they reconnect
or reload. Report that boundary honestly; never equate installing new bytes with reloading every
running model context. New sessions must resolve the local executable/assets. Do not restart an
unrelated host application merely to reload an integration.

Local builds reject `piggery update`, including `--force`. Update the checkout from the fork and
use the build/apply/check workflow instead. Documentation-only changes do not require a daemon
restart unless the installed instruction/assets or the build fingerprint are affected; they
still require applicable validation and the same honest handback.

See [the local development workflow](docs/local-development.md) for commands and recovery.
