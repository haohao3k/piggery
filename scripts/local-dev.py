#!/usr/bin/env python3
"""Build and safely activate the local Piggery checkout.

The command has three public modes:

    scripts/local-dev.sh build
    scripts/local-dev.sh apply [--caller SOLO_ID]
    scripts/local-dev.sh check

``build`` only writes the ignored dist/local candidate. ``apply`` gates the live daemon before
replacing the stable executable, refreshes integrations owned by the current installation, and
restarts that same Piggery home. ``check`` only reads the checkout, candidate, installation and
daemon. No mode downloads a Piggery release or calls a release updater.
"""

from __future__ import annotations

import argparse
import base64
from contextlib import contextmanager
import datetime as _dt
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import stat
import subprocess
import sys
import tempfile
import time
from dataclasses import dataclass
from typing import Any, Iterable, Sequence
from urllib.parse import urlsplit

import fcntl


MODULE = "github.com/sting8k/piggery"
EXPECTED_ORIGIN = "github.com/haohao3k/piggery"
LOCAL_VERSION_PREFIX = "local-"
RECEIPT_SCHEMA = 1
CANDIDATE_DIR = Path("dist") / "local"
CANDIDATE_NAME = "piggery"
CANDIDATE_RECEIPT_NAME = "piggery.receipt.json"
INSTALLED_RECEIPT_NAME = "piggery.local.json"
BACKUP_DIR_NAME = ".piggery-local-backups"
INSTALL_LOCK_NAME = ".piggery-local-dev.lock"
DEFAULT_HOME = Path.home() / ".piggery"
VALID_STATES = {"requested", "starting", "working", "awaiting_permission", "idle", "gone", "parked"}
CALLER_STATES = {"idle", "working"}


class LocalDevError(RuntimeError):
    """A user-actionable local development failure."""


@dataclass(frozen=True)
class BuildInfo:
    root: Path
    candidate: Path
    receipt_path: Path
    receipt: dict[str, Any]


@dataclass(frozen=True)
class GateInfo:
    running: bool
    headless_idle: tuple[str, ...] = ()


@dataclass(frozen=True)
class CheckInfo:
    ok: bool
    lines: tuple[str, ...]
    failures: tuple[str, ...]


def _run(
    args: Sequence[str | os.PathLike[str]],
    *,
    cwd: Path | None = None,
    env: dict[str, str] | None = None,
    check: bool = False,
) -> subprocess.CompletedProcess[str]:
    """Run a command without a shell and capture text output."""

    proc = subprocess.run(
        [str(a) for a in args],
        cwd=str(cwd) if cwd is not None else None,
        env=env,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    if check and proc.returncode != 0:
        detail = proc.stderr.strip() or proc.stdout.strip() or f"exit {proc.returncode}"
        raise LocalDevError(f"{args[0]} failed: {detail}")
    return proc


def _git(root: Path, *args: str, binary: bool = False) -> str | bytes:
    proc = subprocess.run(
        ["git", *args],
        cwd=str(root),
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        check=False,
    )
    if proc.returncode != 0:
        detail = proc.stderr.decode(errors="replace").strip() or f"exit {proc.returncode}"
        raise LocalDevError(f"git {' '.join(args)} failed: {detail}")
    if binary:
        return proc.stdout
    return proc.stdout.decode("utf-8", errors="surrogateescape").strip()


def repo_root(explicit: str | os.PathLike[str] | None = None) -> Path:
    """Resolve the checkout containing this script, independent of the caller's cwd."""

    if explicit:
        root = Path(explicit).expanduser().resolve()
    else:
        root = Path(__file__).resolve().parents[1]
    if not (root / "go.mod").is_file() or not (root / "cmd" / "piggery" / "main.go").is_file():
        raise LocalDevError(f"{root} is not the Piggery checkout (go.mod/cmd/piggery/main.go missing)")
    return root


def _normalise_origin(url: str) -> str:
    """Turn common HTTPS/SSH GitHub remotes into host/path form."""

    value = url.strip()
    if value == "DISABLED":
        return value
    if "://" in value:
        parsed = urlsplit(value)
        host = parsed.hostname or ""
        path = parsed.path
    elif value.startswith("git@") and ":" in value:
        host, path = value[4:].split(":", 1)
    else:
        host, _, path = value.partition("/")
    return (host.lower().strip() + "/" + path.strip("/").removesuffix(".git")).strip("/")


def check_origin(root: Path) -> str:
    """Require the checkout's push origin to be the local fork."""

    url = str(_git(root, "remote", "get-url", "--push", "origin"))
    normalised = _normalise_origin(url)
    if normalised != EXPECTED_ORIGIN:
        raise LocalDevError(
            f"origin is {url!r}; local development requires the push origin "
            f"{EXPECTED_ORIGIN!r}"
        )
    return url


def input_paths(root: Path) -> tuple[str, ...]:
    """List tracked and untracked, non-ignored checkout inputs exactly as Git sees them."""

    raw = _git(root, "ls-files", "--cached", "--others", "--exclude-standard", "-z", binary=True)
    assert isinstance(raw, bytes)
    paths = tuple(sorted(p for p in raw.decode("utf-8", errors="surrogateescape").split("\0") if p))
    if not paths:
        raise LocalDevError("git reported no checkout inputs")
    return paths


def source_digest(root: Path) -> str:
    """Hash path names and worktree bytes, including non-ignored untracked files."""

    digest = hashlib.sha256()
    for rel in input_paths(root):
        digest.update(rel.encode("utf-8", errors="surrogateescape"))
        digest.update(b"\0")
        relative = Path(rel)
        if relative.is_absolute() or ".." in relative.parts:
            raise LocalDevError(f"input {rel} escapes the checkout; remove it before local development")
        path = root
        parts = relative.parts
        for index, part in enumerate(parts):
            path = path / part
            try:
                info = path.lstat()
            except FileNotFoundError:
                # A deleted tracked input must change the digest and then fail the build normally.
                data = b"<missing>"
                break
            except OSError as exc:
                raise LocalDevError(f"cannot inspect input {rel}: {exc}") from exc
            if stat.S_ISLNK(info.st_mode):
                raise LocalDevError(
                    f"input {rel} traverses symlink {path.relative_to(root)}; "
                    "remove it before local development"
                )
            if index < len(parts) - 1:
                if not stat.S_ISDIR(info.st_mode):
                    raise LocalDevError(f"input {rel} has a non-directory parent; refusing to read it")
            elif not stat.S_ISREG(info.st_mode):
                raise LocalDevError(f"input {rel} is not a regular file; refusing to read it")
        else:
            try:
                data = path.read_bytes()
            except OSError as exc:
                raise LocalDevError(f"cannot read input {rel}: {exc}") from exc
        digest.update(len(data).to_bytes(8, "big"))
        digest.update(data)
    return digest.hexdigest()


def commit_id(root: Path) -> str:
    value = str(_git(root, "rev-parse", "--short=12", "HEAD"))
    if not value:
        raise LocalDevError("cannot stamp a local build without a Git commit")
    return value


def local_version(root: Path, digest: str) -> tuple[str, str]:
    commit = commit_id(root)
    return f"{LOCAL_VERSION_PREFIX}{commit}-{digest}", commit


def candidate_paths(root: Path) -> tuple[Path, Path]:
    directory = root / CANDIDATE_DIR
    return directory / CANDIDATE_NAME, directory / CANDIDATE_RECEIPT_NAME


def install_dir() -> Path:
    value = os.environ.get("PIGGERY_INSTALL_DIR", "")
    return Path(value).expanduser() if value else Path.home() / ".local" / "bin"


def installed_paths() -> tuple[Path, Path]:
    directory = install_dir()
    return directory / CANDIDATE_NAME, directory / INSTALLED_RECEIPT_NAME


def piggery_home() -> Path:
    """Return the only home the current binary supports, rejecting pretend overrides."""

    value = os.environ.get("PIGGERY_HOME", "").strip()
    if value:
        requested = Path(value).expanduser().resolve()
        default = DEFAULT_HOME.expanduser().resolve()
        if requested != default:
            raise LocalDevError(
                f"PIGGERY_HOME={value!r} is unsupported by this build; "
                f"server.DefaultDir uses {default}"
            )
    return DEFAULT_HOME


def _sha256_file(path: Path) -> str:
    _require_regular(path, "file")
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            digest.update(block)
    return digest.hexdigest()


def _require_regular(path: Path, label: str) -> os.stat_result:
    """Reject missing, symlinked, or special files before any read, copy, or probe."""

    try:
        info = path.lstat()
    except FileNotFoundError as exc:
        raise LocalDevError(f"{label} {path} is missing") from exc
    if not stat.S_ISREG(info.st_mode):
        raise LocalDevError(f"{label} {path} is not a regular file; refusing to use it")
    return info


def _read_json(path: Path) -> dict[str, Any] | None:
    try:
        path.lstat()
    except FileNotFoundError:
        return None
    except OSError as exc:
        raise LocalDevError(f"cannot inspect receipt {path}: {exc}") from exc
    _require_regular(path, "receipt")
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise LocalDevError(f"cannot read receipt {path}: {exc}") from exc
    if not isinstance(value, dict):
        raise LocalDevError(f"receipt {path} is not a JSON object")
    return value


def _fsync_dir(path: Path) -> None:
    try:
        fd = os.open(path, os.O_RDONLY)
    except OSError:
        return
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def _write_json_atomic(path: Path, value: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temp_name = tempfile.mkstemp(prefix=f".{path.name}.", suffix=".tmp", dir=path.parent)
    temp = Path(temp_name)
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(value, stream, indent=2, sort_keys=True)
            stream.write("\n")
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temp, path)
        _fsync_dir(path.parent)
    finally:
        temp.unlink(missing_ok=True)


def _go_version(go: str, root: Path) -> str:
    proc = _run([go, "version"], cwd=root)
    if proc.returncode != 0:
        detail = proc.stderr.strip() or proc.stdout.strip() or f"exit {proc.returncode}"
        raise LocalDevError(f"{go} version failed: {detail}")
    return proc.stdout.strip()


def _receipt_for_build(
    *,
    root: Path,
    version: str,
    commit: str,
    digest: str,
    binary_sha: str,
    go_version: str,
) -> dict[str, Any]:
    return {
        "schema": RECEIPT_SCHEMA,
        "build_mode": "local",
        "source_root": str(root.resolve()),
        "state": "candidate",
        "version": version,
        "commit": commit,
        "source_digest": digest,
        "binary_sha256": binary_sha,
        "go_version": go_version,
    }


def build(root: Path) -> BuildInfo:
    """Build a candidate and refuse a source tree that changed during the build."""

    check_origin(root)
    digest = source_digest(root)
    version, commit = local_version(root, digest)
    candidate, receipt_path = candidate_paths(root)
    candidate.parent.mkdir(parents=True, exist_ok=True)
    go = os.environ.get("GO", "go")
    go_version = _go_version(go, root)
    env = os.environ.copy()
    # Match release artifacts unless the caller intentionally selected another value.
    env.setdefault("CGO_ENABLED", "0")
    fd, temp_name = tempfile.mkstemp(prefix=".piggery-build-", dir=candidate.parent)
    os.close(fd)
    temp = Path(temp_name)
    try:
        ldflags = (
            f"-s -w -X {MODULE}/internal/cli.Version={version} "
            f"-X {MODULE}/internal/cli.BuildMode=local "
            f"-X {MODULE}/internal/cli.LocalSourceRootBase64="
            + base64.b64encode(os.fsencode(root.resolve())).decode("ascii")
        )
        proc = _run(
            [go, "build", "-trimpath", "-ldflags", ldflags, "-o", temp, "./cmd/piggery"],
            cwd=root,
            env=env,
        )
        if proc.returncode != 0:
            detail = proc.stderr.strip() or proc.stdout.strip() or f"exit {proc.returncode}"
            raise LocalDevError(f"go build failed: {detail}")
        after = source_digest(root)
        if after != digest:
            raise LocalDevError(
                "source changed during build; candidate discarded (run local-dev.sh build again)"
            )
        os.chmod(temp, 0o755)
        binary_sha = _sha256_file(temp)
        receipt = _receipt_for_build(
            root=root,
            version=version,
            commit=commit,
            digest=digest,
            binary_sha=binary_sha,
            go_version=go_version,
        )
        os.replace(temp, candidate)
        _fsync_dir(candidate.parent)
        _write_json_atomic(receipt_path, receipt)
    finally:
        temp.unlink(missing_ok=True)
    return BuildInfo(root, candidate, receipt_path, receipt)


def _candidate_matches(root: Path, digest: str, commit: str) -> BuildInfo | None:
    candidate, receipt_path = candidate_paths(root)
    receipt = _read_json(receipt_path)
    if receipt is None or not os.path.lexists(candidate):
        return None
    _require_regular(candidate, "candidate")
    try:
        sha = _sha256_file(candidate)
    except OSError:
        return None
    version = f"{LOCAL_VERSION_PREFIX}{commit}-{digest}"
    expected = {
        "schema": RECEIPT_SCHEMA,
        "build_mode": "local",
        "source_root": str(root.resolve()),
        "state": "candidate",
        "version": version,
        "commit": commit,
        "source_digest": digest,
        "binary_sha256": sha,
    }
    if any(receipt.get(key) != value for key, value in expected.items()):
        return None
    if not os.access(candidate, os.X_OK):
        return None
    return BuildInfo(root, candidate, receipt_path, receipt)


def _candidate_or_build(root: Path) -> BuildInfo:
    digest = source_digest(root)
    commit = commit_id(root)
    current = _candidate_matches(root, digest, commit)
    if current is not None:
        print(f"reusing candidate {current.candidate} ({current.receipt['version']})")
        return current
    print("candidate is absent or stale; building")
    return build(root)


def _command_env() -> dict[str, str]:
    env = os.environ.copy()
    env.setdefault("NO_COLOR", "1")
    return env


def _cli_output(proc: subprocess.CompletedProcess[str], label: str) -> dict[str, Any]:
    stdout = proc.stdout or ""
    stderr = proc.stderr or ""
    if stdout:
        print(stdout, end="" if stdout.endswith("\n") else "\n")
    if proc.returncode != 0:
        detail = stderr.strip() or stdout.strip() or f"exit {proc.returncode}"
        raise LocalDevError(f"{label} failed: {detail}")
    payload = (stdout + "\n" + stderr).encode("utf-8", errors="replace")
    return {
        "command": label,
        "status": "ok",
        "output_sha256": hashlib.sha256(payload).hexdigest(),
        "output_bytes": len(payload),
        "output_lines": len(stdout.splitlines()) + len(stderr.splitlines()),
    }


def _refresh_evidence_ok(value: Any) -> bool:
    if not isinstance(value, dict) or value.get("status") != "ok":
        return False
    steps = value.get("steps")
    if not isinstance(steps, list) or not steps:
        return False
    for step in steps:
        if not isinstance(step, dict) or step.get("status") != "ok":
            return False
        if not isinstance(step.get("command"), str) or not step["command"].strip():
            return False
        digest = step.get("output_sha256")
        if not isinstance(digest, str) or re.fullmatch(r"[0-9a-f]{64}", digest) is None:
            return False
        if (
            not isinstance(step.get("output_bytes"), int)
            or step["output_bytes"] < 0
            or not isinstance(step.get("output_lines"), int)
            or step["output_lines"] < 0
        ):
            return False
    return True


def _validate_snapshot(value: Any) -> dict[str, Any]:
    """Validate the minimum ps shape before treating a snapshot as an idle readback."""

    if not isinstance(value, dict):
        raise LocalDevError("piggery ps --json returned a JSON value, not an object")
    pid = value.get("pid")
    started_at = value.get("started_at")
    version = value.get("version")
    if not isinstance(pid, int) or isinstance(pid, bool) or pid <= 0:
        raise LocalDevError("piggery ps --json has no valid positive pid; daemon state is unknown")
    if not isinstance(started_at, int) or isinstance(started_at, bool) or started_at <= 0:
        raise LocalDevError(
            "piggery ps --json has no valid started_at; daemon state is unknown"
        )
    if not isinstance(version, str) or not version.strip():
        raise LocalDevError("piggery ps --json has no nonempty version; daemon state is unknown")
    for collection in ("teams", "solos"):
        rows = value.get(collection)
        if not isinstance(rows, list):
            raise LocalDevError(
                f"piggery ps --json has no valid {collection} list; daemon state is unknown"
            )
        for row in rows:
            if not isinstance(row, dict) or not row.get("id") or not row.get("name"):
                raise LocalDevError(
                    f"piggery ps --json has an invalid {collection} entry; daemon state is unknown"
                )
            if collection == "teams":
                members = row.get("members")
                if not isinstance(members, list):
                    raise LocalDevError(
                        "piggery ps --json has an invalid team members list; daemon state is unknown"
                    )
                for member in members:
                    if not isinstance(member, dict) or not member.get("id") or not member.get("name"):
                        raise LocalDevError("piggery ps --json has an invalid member; daemon state is unknown")
                    state = member.get("state")
                    if state not in VALID_STATES:
                        raise LocalDevError(
                            f"piggery ps --json has unknown member state {state!r}; daemon state is unknown"
                        )
            else:
                state = row.get("state")
                if state not in VALID_STATES:
                    raise LocalDevError(
                        f"piggery ps --json has unknown solo state {state!r}; daemon state is unknown"
                    )
    return value


def _daemon_snapshot(binary: Path | None) -> dict[str, Any] | None:
    if binary is None or not os.path.lexists(binary):
        if piggery_home().joinpath("piggery.sock").exists():
            raise LocalDevError("daemon state is unknown: piggery.sock exists but no probe binary is available")
        return None
    _require_regular(binary, "probe binary")
    proc = _run(
        [binary, "ps", "--admin", "--json", "--no-start"],
        env=_command_env(),
    )
    if proc.returncode != 0:
        detail = (proc.stderr + "\n" + proc.stdout).lower()
        if "daemon is not running" in detail or "not running" in detail:
            return None
        message = proc.stderr.strip() or proc.stdout.strip() or f"exit {proc.returncode}"
        raise LocalDevError(f"cannot read daemon state without starting it: {message}")
    try:
        value = json.loads(proc.stdout)
    except json.JSONDecodeError as exc:
        raise LocalDevError(f"piggery ps --json returned invalid JSON: {exc}") from exc
    return _validate_snapshot(value)


def _probe_binary(root: Path) -> Path | None:
    """Prefer stable, then candidate; both address the same default Piggery home."""

    installed, _ = installed_paths()
    if os.path.lexists(installed):
        _require_regular(installed, "installed binary probe")
        return installed
    candidate, _ = candidate_paths(root)
    if os.path.lexists(candidate):
        _require_regular(candidate, "candidate probe")
        return candidate
    return None


def _state_rows(snapshot: dict[str, Any]) -> Iterable[tuple[str, str, str, str, bool]]:
    """Yield (kind, id, display, state, headless) for teams and solos."""

    for team in snapshot.get("teams", []):
        if not isinstance(team, dict):
            continue
        team_name = str(team.get("name", team.get("id", "team")))
        for member in team.get("members", []):
            if not isinstance(member, dict):
                continue
            ident = str(member.get("id", ""))
            name = str(member.get("name", ident))
            yield "member", ident, f"{team_name}/{name}", str(member.get("state", "unknown")), bool(member.get("headless"))
    for solo in snapshot.get("solos", []):
        if not isinstance(solo, dict):
            continue
        ident = str(solo.get("id", ""))
        name = str(solo.get("name", ident))
        yield "solo", ident, name, str(solo.get("state", "unknown")), False


def idle_gate(snapshot: dict[str, Any] | None, caller: str | None) -> GateInfo:
    """Require every non-gone participant to be idle, except the exact invoking solo ID."""

    if snapshot is None:
        if caller:
            raise LocalDevError("--caller requires a live daemon so its exact solo can be verified")
        return GateInfo(False)

    snapshot = _validate_snapshot(snapshot)

    rows = list(_state_rows(snapshot))
    if caller:
        matching = [state for kind, ident, _, state, _ in rows if kind == "solo" and ident == caller]
        if not matching or matching[0] not in CALLER_STATES:
            raise LocalDevError(
                f"--caller {caller!r} must be the exact ID of a live solo in idle or working state"
            )

    busy: list[str] = []
    headless_idle: list[str] = []
    allowed = {"idle", "gone"}
    for kind, ident, display, state, headless in rows:
        if state not in allowed and not (kind == "solo" and ident == caller):
            busy.append(f"{display} ({ident or 'unknown id'}) state={state}")
        elif kind == "member" and headless and state == "idle":
            headless_idle.append(f"{display} ({ident or 'unknown id'})")
    if busy:
        raise LocalDevError(
            "idle gate blocked by non-idle participants: " + "; ".join(busy)
        )
    return GateInfo(True, tuple(headless_idle))


def _backup_existing(binary: Path, receipt: Path) -> Path | None:
    if not os.path.lexists(binary) and not os.path.lexists(receipt):
        return None
    backup_root = binary.parent / BACKUP_DIR_NAME
    backup = backup_root / time.strftime("%Y%m%d-%H%M%S", time.localtime())
    suffix = 0
    while backup.exists():
        suffix += 1
        backup = backup_root / f"{time.strftime('%Y%m%d-%H%M%S', time.localtime())}-{suffix}"
    backup.mkdir(parents=True, exist_ok=False)
    if os.path.lexists(binary):
        _require_regular(binary, "installed binary backup")
        shutil.copy2(binary, backup / binary.name, follow_symlinks=True)
    if os.path.lexists(receipt):
        _require_regular(receipt, "installed receipt backup")
        shutil.copy2(receipt, backup / receipt.name, follow_symlinks=True)
    return backup


@contextmanager
def install_lock() -> Iterable[None]:
    """Serialize apply operations that replace the stable binary and receipt."""

    directory = install_dir()
    directory.mkdir(parents=True, exist_ok=True)
    path = directory / INSTALL_LOCK_NAME
    stream = path.open("a+")
    try:
        try:
            fcntl.flock(stream.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError as exc:
            raise LocalDevError(f"another local-dev apply is running (lock: {path})") from exc
        yield
    finally:
        try:
            fcntl.flock(stream.fileno(), fcntl.LOCK_UN)
        finally:
            stream.close()


def _atomic_copy(source: Path, target: Path, mode: int = 0o755) -> None:
    _require_regular(source, "candidate")
    target.parent.mkdir(parents=True, exist_ok=True)
    fd, temp_name = tempfile.mkstemp(prefix=f".{target.name}.", suffix=".tmp", dir=target.parent)
    temp = Path(temp_name)
    try:
        with os.fdopen(fd, "wb") as out, source.open("rb") as inp:
            shutil.copyfileobj(inp, out)
            out.flush()
            os.fchmod(out.fileno(), mode)
            os.fsync(out.fileno())
        os.replace(temp, target)
        _fsync_dir(target.parent)
    finally:
        temp.unlink(missing_ok=True)


def install_candidate(info: BuildInfo) -> tuple[Path, Path | None, dict[str, Any]]:
    binary, receipt_path = installed_paths()
    backup = _backup_existing(binary, receipt_path)
    _atomic_copy(info.candidate, binary, stat.S_IMODE(info.candidate.stat().st_mode) or 0o755)
    pending = dict(info.receipt)
    pending["state"] = "pending"
    pending["activation"] = {"state": "pending"}
    _write_json_atomic(receipt_path, pending)
    return binary, backup, pending


def _installed_version(binary: Path) -> str:
    _require_regular(binary, "installed binary")
    proc = _run([binary, "--version"], env=_command_env())
    if proc.returncode != 0:
        detail = proc.stderr.strip() or proc.stdout.strip() or f"exit {proc.returncode}"
        raise LocalDevError(f"{binary} --version failed: {detail}")
    text = proc.stdout.strip()
    match = re.search(r"piggery version\s+([^\s]+)", text, re.IGNORECASE)
    if match:
        return match.group(1)
    if text:
        return text.splitlines()[-1].strip()
    raise LocalDevError(f"{binary} --version returned no version")


def inspect(root: Path) -> CheckInfo:
    """Read all build/install/runtime receipts and report every mismatch."""

    current_digest = source_digest(root)
    current_commit = commit_id(root)
    expected_version = f"{LOCAL_VERSION_PREFIX}{current_commit}-{current_digest}"
    candidate, candidate_receipt_path = candidate_paths(root)
    installed, installed_receipt_path = installed_paths()
    lines: list[str] = [f"checkout: {root.resolve()}", f"source digest: {current_digest}", f"commit: {current_commit}"]
    failures: list[str] = []

    candidate_receipt = _read_json(candidate_receipt_path)
    if candidate_receipt is None or not os.path.lexists(candidate):
        failures.append("candidate is missing")
        lines.append("candidate: MISSING")
    else:
        _require_regular(candidate, "candidate")
        candidate_sha = _sha256_file(candidate)
        candidate_ok = (
            candidate_receipt.get("schema") == RECEIPT_SCHEMA
            and candidate_receipt.get("build_mode") == "local"
            and candidate_receipt.get("source_root") == str(root.resolve())
            and candidate_receipt.get("state") == "candidate"
            and candidate_receipt.get("version") == expected_version
            and candidate_receipt.get("commit") == current_commit
            and candidate_receipt.get("source_digest") == current_digest
            and candidate_receipt.get("binary_sha256") == candidate_sha
            and os.access(candidate, os.X_OK)
        )
        if not candidate_ok:
            failures.append("candidate receipt, SHA, or source digest is stale")
            lines.append("candidate: STALE")
        else:
            lines.append(f"candidate: OK sha256={candidate_sha} version={expected_version}")

    installed_receipt = _read_json(installed_receipt_path)
    if not os.path.lexists(installed):
        failures.append(f"installed binary is missing: {installed}")
        lines.append("installed: MISSING")
    elif installed_receipt is None:
        failures.append(f"installed receipt is missing: {installed_receipt_path}")
        lines.append("installed: receipt missing")
    else:
        installed_sha = _sha256_file(installed)
        installed_version = _installed_version(installed)
        candidate_sha = None
        if candidate_receipt is not None and os.path.lexists(candidate):
            _require_regular(candidate, "candidate")
            candidate_sha = _sha256_file(candidate)
        activation = installed_receipt.get("activation")
        refresh_evidence = activation.get("refresh") if isinstance(activation, dict) else None
        activation_ok = (
            isinstance(activation, dict)
            and activation.get("state") == "activated"
            and activation.get("daemon_version") == expected_version
            and _refresh_evidence_ok(refresh_evidence)
        )
        installed_ok = (
            installed_receipt.get("schema") == RECEIPT_SCHEMA
            and installed_receipt.get("build_mode") == "local"
            and installed_receipt.get("source_root") == str(root.resolve())
            and installed_receipt.get("state") == "activated"
            and activation_ok
            and installed_receipt.get("version") == expected_version
            and installed_receipt.get("source_digest") == current_digest
            and installed_receipt.get("binary_sha256") == installed_sha
            and candidate_sha is not None
            and installed_sha == candidate_sha
            and installed_version == expected_version
        )
        if not installed_ok:
            failures.append("installed activation, receipt, SHA, version, or source digest is stale")
            lines.append(
                f"installed: STALE sha256={installed_sha} version={installed_version}"
            )
        else:
            lines.append(f"installed: OK sha256={installed_sha} version={installed_version}")

    snapshot: dict[str, Any] | None = None
    probe = _probe_binary(root)
    if probe is not None or piggery_home().joinpath("piggery.sock").exists():
        snapshot = _daemon_snapshot(probe)
    if snapshot is None:
        failures.append("daemon is not running")
        lines.append("daemon: not running")
    else:
        daemon_version = str(snapshot.get("version", ""))
        if daemon_version != expected_version:
            failures.append(f"daemon version is {daemon_version or '(empty)'}, expected {expected_version}")
            lines.append(f"daemon: STALE version={daemon_version or '(empty)'}")
        else:
            lines.append(f"daemon: OK version={daemon_version}")
    return CheckInfo(not failures, tuple(lines), tuple(failures))


def _print_check(result: CheckInfo) -> None:
    for line in result.lines:
        print(line)
    if result.failures:
        for failure in result.failures:
            print(f"check: {failure}", file=sys.stderr)


def _activate_receipt(
    receipt_path: Path,
    pending: dict[str, Any],
    snapshot: dict[str, Any],
    headless_idle: Sequence[str] = (),
    refresh_evidence: dict[str, Any] | None = None,
) -> dict[str, Any]:
    snapshot = _validate_snapshot(snapshot)
    daemon_version = str(snapshot.get("version", ""))
    if daemon_version != pending.get("version"):
        raise LocalDevError(
            f"daemon version is {daemon_version or '(empty)'}, expected {pending.get('version')}"
        )
    if not _refresh_evidence_ok(refresh_evidence):
        raise LocalDevError("cannot activate without successful setup --refresh evidence")
    activated = dict(pending)
    activated["state"] = "activated"
    activated["activation"] = {
        "state": "activated",
        "daemon_version": daemon_version,
        "daemon_pid": snapshot.get("pid"),
        "headless_idle": list(headless_idle),
        "refresh": refresh_evidence,
        "at": _dt.datetime.now(_dt.timezone.utc).isoformat(),
    }
    _write_json_atomic(receipt_path, activated)
    return activated


def _mark_activation_failed(receipt_path: Path, activated: dict[str, Any], reason: str) -> None:
    failed = dict(activated)
    activation = dict(activated.get("activation") or {})
    activation["state"] = "failed"
    activation["failure"] = reason
    failed["state"] = "failed"
    failed["activation"] = activation
    _write_json_atomic(receipt_path, failed)


def _refresh_and_restart(
    root: Path, binary: Path, caller: str | None
) -> tuple[dict[str, Any], tuple[str, ...], dict[str, Any]]:
    home = piggery_home()
    steps: list[dict[str, Any]] = []
    # setup --refresh is the normal path for an initialized home: it updates only integrations
    # already installed and safely unpacks built-in assets. A fresh home still needs profiles and
    # config defaults once; setup without --force leaves existing user values alone.
    if not (home / "config.yaml").is_file() or not (home / "harness").is_dir():
        proc = _run([binary, "setup"], env=_command_env())
        steps.append(_cli_output(proc, "piggery setup"))
    proc = _run([binary, "setup", "--refresh"], env=_command_env())
    steps.append(_cli_output(proc, "piggery setup --refresh"))
    # Setup may invoke harnesses and change participant state; take a fresh readback immediately
    # before restarting instead of relying on the pre-install gate.
    late_snapshot = _daemon_snapshot(_probe_binary(root))
    late_gate = idle_gate(late_snapshot, caller)
    if late_gate.headless_idle:
        print(
            "idle gate before restart: restart will gracefully stop idle headless workers "
            "(never auto-resume): " + ", ".join(late_gate.headless_idle)
        )
    else:
        print("idle gate before restart: passed")
    # Restart is an admin command. Do not inherit a participant's PIGGERY_ID/TOKEN and let the
    # command fail after the candidate has already replaced the stable executable.
    proc = _run([binary, "--admin", "restart"], env=_command_env())
    _cli_output(proc, "piggery restart")
    snapshot = _daemon_snapshot(binary)
    if snapshot is None:
        raise LocalDevError("piggery restart returned but the daemon is not running")
    return snapshot, late_gate.headless_idle, {"status": "ok", "steps": steps}


def apply(root: Path, caller: str | None) -> None:
    check_origin(root)
    with install_lock():
        info = _candidate_or_build(root)
        before = source_digest(root)
        if before != info.receipt.get("source_digest"):
            raise LocalDevError("source changed after the candidate build; run local-dev.sh build again")
        gate = idle_gate(_daemon_snapshot(_probe_binary(root)), caller)
        # Read once more immediately before replacement; a new busy session must stop the apply.
        if source_digest(root) != before:
            raise LocalDevError("source changed before replacement; candidate is stale")
        if gate.headless_idle:
            print(
                "idle gate: restart will gracefully stop idle headless workers (never auto-resume): "
                + ", ".join(gate.headless_idle)
            )
        else:
            print("idle gate: passed")
        installed, backup, pending = install_candidate(info)
        print(f"installed {info.receipt['version']} at {installed} (activation pending)")
        if backup is not None:
            print(f"backup: {backup}")
        snapshot, headless_idle, refresh_evidence = _refresh_and_restart(root, installed, caller)
        after = source_digest(root)
        if after != before:
            raise LocalDevError("source changed after installation; rerun local-dev.sh build and apply")
        _, installed_receipt_path = installed_paths()
        activated = _activate_receipt(
            installed_receipt_path, pending, snapshot, headless_idle, refresh_evidence
        )
        try:
            result = inspect(root)
        except (LocalDevError, OSError):
            _mark_activation_failed(installed_receipt_path, activated, "post-apply verification failed")
            raise
        _print_check(result)
        if not result.ok:
            _mark_activation_failed(installed_receipt_path, activated, "post-apply verification failed")
            raise LocalDevError("post-apply check failed")


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(description="build, apply, or check the local Piggery fork")
    parser.add_argument("mode", choices=("build", "apply", "check"))
    parser.add_argument("--caller", help="exact solo participant ID allowed to remain active during apply")
    # These are intentionally useful for isolated tests and alternate checkouts; normal use relies
    # on the script's own repository and PIGGERY_INSTALL_DIR.
    parser.add_argument("--root", help=argparse.SUPPRESS)
    return parser


def main(argv: Sequence[str] | None = None) -> int:
    parser = _parser()
    args = parser.parse_args(argv)
    try:
        root = repo_root(args.root)
        piggery_home()
        check_origin(root)
        if args.mode == "build":
            info = build(root)
            print(f"built {info.candidate}")
            print(f"version: {info.receipt['version']}")
            print(f"source digest: {info.receipt['source_digest']}")
            print(f"sha256: {info.receipt['binary_sha256']}")
            return 0
        if args.mode == "check":
            result = inspect(root)
            _print_check(result)
            return 0 if result.ok else 1
        apply(root, args.caller)
        return 0
    except (LocalDevError, OSError) as exc:
        print(f"local-dev: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
