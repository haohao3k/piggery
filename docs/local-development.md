# Local fork development

The development remote is `haohao3k/piggery`. The checkout supplies the executable and embedded
assets used on this machine. Upstream remains a fetch-only reference. Keep the Go module import
path unchanged; it is an import identity, not an instruction to download the upstream executable.

## Routine change

```sh
go test ./...                         # narrow this when a focused check is appropriate
./scripts/local-dev.sh build
./scripts/local-dev.sh apply
./scripts/local-dev.sh check
```

This is an explicit developer workflow. `piggery update` uses GitHub Releases from
`haohao3k/piggery` and never invokes these scripts. Use `scripts/local-dev.sh check` to verify
a development deployment; `piggery update --check` only compares published release versions.

`build` creates a candidate in the ignored `dist/local` directory. It fingerprints checkout inputs,
including local source and embedded assets, and stamps a unique development version. A fingerprint
mismatch requires another build. `apply` builds the current candidate, checks
activation safety, saves the previous binary, installs atomically at the stable path, refreshes
assets and restarts the same Piggery home. `check` is read-only and must fail when source, installed
binary or daemon no longer matches the activation receipt.

When the installer itself is running inside Piggery, pass only its exact current solo participant
ID to `apply --caller <participant-id>`. This permits that one session to perform the maintenance
it was assigned. It does not exempt another session or any headless worker. Obtain the ID from
the current identity/readback, not from a saved name or old task.

An agent reporting a change complete must include the final check outcome. Source/tests without
activation are reported separately as **BLOCKED activation**, with the actual blocking participants
or failed readback. Never stop busy agents merely to turn that result green. The source/test work
can continue while the runtime is busy. Ordinary safe activation is already authorized by this
repository's working agreement; do not add another approval ritual.

## What activation preserves and changes

The existing Piggery home, config, profiles, database, messages and team snapshots are retained.
`setup --refresh` uses dedicated asset refresh paths for existing integrations, even when their
integration-version integer is unchanged. It preserves host registration, enablement, worker
profiles and Pi's selected extension mode. Disabled or unregistered integrations are reported
and left alone. Existing custom templates stay custom; refreshed
built-ins that still differ are reported by name as preserved local overrides. Review such a
message when the changed asset is expected to apply to that template.

A fresh authoritative state check happens before executable replacement. Busy, starting,
awaiting-permission and unknown participants block activation, except the explicitly named invoking
solo. Idle headless workers are stopped by the normal restart; Piggery records their exits and
retains their sessions. The activation receipt lists them. The installer never resumes workers or
starts a model/provider run. A later authorized task may resume the same participant normally.

The gate reads state before installation and again immediately before restart. It is an
observational check, not a server maintenance lock: a new turn can start after the final read.
Schedule activation during a quiet interval; an atomic server-side maintenance gate remains a
separate orchestration improvement.

Frozen team manifests do not change when their template is refreshed. This preserves existing team
contracts; a new team receives the refreshed template. A currently running MCP process, Pi session
or host app may also retain loaded adapter/instruction bytes until it reconnects or reloads. A
successful file refresh is not proof that every existing model context changed. New processes
resolve the stable local executable and refreshed assets. Follow setup's reload guidance without
restarting an unrelated host application.

## Assets and upgrade protection

The CLI skill text, templates/prompts, and harness extensions are embedded in the Go binary.
Editing those checkout files requires a build. Version-based `setup --outdated` remains useful for
release installations, but it cannot by itself guarantee that same-version development assets were
refreshed. The local workflow explicitly runs `setup --refresh` for that reason.

The public `install.sh` and `piggery update` download fork release binaries and validate their
checksums. They need no checkout or installation receipt. `--force` permits replacing a development
build with a fork release. These release commands do not perform this script's source fingerprint
verification or guarded activation; use the explicit script when validating development changes.
A successful release update replaces the executable and stops the old daemon; the next command
starts the new one. Run `piggery setup --outdated` and reconnect affected integrations afterwards.

Only `haohao3k/piggery` supplies releases for this fork. A push to main alone supplies no binary:
the release workflow must publish the platform assets and `checksums.txt`. A missing release is an
error, never a reason to download from upstream. The unchanged Go module path is an import identity.

## Git and recovery

Use `origin` for this fork and `codex/` branches. Keep unrelated work separate. `upstream` can be
fetched to inspect changes, but its push URL stays disabled. A release or merge remains a distinct
Human-authorized action; pushing a development branch does not imply either.

An apply that stops before replacement leaves the installed runtime unchanged. After replacement,
an asset-refresh or restart failure must remain a failed activation; do not write a successful
receipt. Inspect the reported stage, retain the backup, and retry the corrected local candidate
when the daemon can be safely restarted. Rollback restores the previous executable, not a copied
credential/database directory; any asset changes also need the corresponding prior build's guarded
refresh. Keep backups and receipts outside Git and never paste auth material into a handback.
If the candidate migrated the schema, do not start the previous executable against the upgraded
database. Prefer a corrected candidate; a deliberate rollback must account for the store's
pre-migration backup and any activity after it. See [session isolation](session-isolation.md).

## Returning an existing fork-23 home to upstream

The runtime uses upstream schema 27. Native upstream schemas 23–27 are recognized by their
exact schema shape and left to the runtime migrations. `apply` automatically handles only the known older fork-23
schema after the idle gate: graceful shutdown, exclusive daemon lock, full SQLite backup,
retirement of gone ambiguous identities, and transactional removal of the fork column, returning to upstream 22 before the runtime migrates to 27. The
backup path is recorded in the activation receipt. Unknown schemas block installation.
See [session isolation](session-isolation.md) for the data-preservation contract.
Do not restore only the old binary after conversion: it would add the fork migration again.
Recovery requires the matching binary and database backup with the daemon stopped.
