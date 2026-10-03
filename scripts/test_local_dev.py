#!/usr/bin/env python3
"""Focused tests for the local-dev safety gates and receipts."""

from __future__ import annotations

import importlib.util
import json
import os
from pathlib import Path
import sys
import tempfile
import unittest
from unittest import mock


SCRIPT = Path(__file__).with_name("local-dev.py")
SPEC = importlib.util.spec_from_file_location("piggery_local_dev", SCRIPT)
assert SPEC and SPEC.loader
local_dev = importlib.util.module_from_spec(SPEC)
sys.modules[SPEC.name] = local_dev
SPEC.loader.exec_module(local_dev)


def valid_snapshot(*, version="local-test", teams=None, solos=None):
    return {
        "pid": 1,
        "started_at": 1,
        "version": version,
        "teams": [] if teams is None else teams,
        "solos": [] if solos is None else solos,
    }


def valid_refresh_evidence():
    return {
        "status": "ok",
        "steps": [{
            "command": "piggery setup --refresh",
            "status": "ok",
            "output_sha256": "0" * 64,
            "output_bytes": 0,
            "output_lines": 0,
        }],
    }


class SourceDigestTests(unittest.TestCase):
    def test_digest_changes_for_untracked_nonignored_input(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "checkout"
            root.mkdir()
            source = root / "source.go"
            source.write_text("package main\n", encoding="utf-8")
            with mock.patch.object(local_dev, "input_paths", return_value=("source.go",)):
                first = local_dev.source_digest(root)
                source.write_text("package main\nvar changed = true\n", encoding="utf-8")
                second = local_dev.source_digest(root)
            self.assertNotEqual(first, second)

    def test_digest_rejects_fifo_and_symlink_before_reading(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp) / "checkout"
            root.mkdir()
            fifo = root / "input.fifo"
            os.mkfifo(fifo)
            with mock.patch.object(local_dev, "input_paths", return_value=("input.fifo",)):
                with self.assertRaisesRegex(local_dev.LocalDevError, "not a regular file"):
                    local_dev.source_digest(root)
            outside = root.parent / "outside-source"
            outside.write_text("secret", encoding="utf-8")
            link = root / "link.go"
            link.symlink_to(outside)
            with mock.patch.object(local_dev, "input_paths", return_value=("link.go",)):
                with self.assertRaisesRegex(local_dev.LocalDevError, "symlink"):
                    local_dev.source_digest(root)
            outside_dir = root.parent / "outside-dir"
            outside_dir.mkdir()
            (outside_dir / "source.go").write_text("package outside\n", encoding="utf-8")
            parent_link = root / "linked-dir"
            parent_link.symlink_to(outside_dir, target_is_directory=True)
            with mock.patch.object(local_dev, "input_paths", return_value=("linked-dir/source.go",)):
                with self.assertRaisesRegex(local_dev.LocalDevError, "traverses symlink"):
                    local_dev.source_digest(root)


class FileSafetyTests(unittest.TestCase):
    def test_receipt_binary_backup_and_probe_reject_special_files(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            receipt_fifo = root / "receipt"
            binary_fifo = root / "binary"
            os.mkfifo(receipt_fifo)
            os.mkfifo(binary_fifo)
            with self.assertRaisesRegex(local_dev.LocalDevError, "not a regular file"):
                local_dev._read_json(receipt_fifo)
            with self.assertRaisesRegex(local_dev.LocalDevError, "not a regular file"):
                local_dev._sha256_file(binary_fifo)
            with self.assertRaisesRegex(local_dev.LocalDevError, "not a regular file"):
                local_dev._atomic_copy(binary_fifo, root / "copy")
            with self.assertRaisesRegex(local_dev.LocalDevError, "not a regular file"):
                local_dev._daemon_snapshot(binary_fifo)
            regular = root / "installed"
            regular.write_bytes(b"old")
            with self.assertRaisesRegex(local_dev.LocalDevError, "not a regular file"):
                local_dev._backup_existing(regular, receipt_fifo)
            with mock.patch.object(local_dev, "installed_paths", return_value=(binary_fifo, root / "receipt.json")):
                with self.assertRaisesRegex(local_dev.LocalDevError, "not a regular file"):
                    local_dev._probe_binary(root)


class OriginTests(unittest.TestCase):
    def test_normalise_common_fork_urls(self) -> None:
        for url in (
            "https://github.com/haohao3k/piggery.git",
            "git@github.com:haohao3k/piggery.git",
            "ssh://git@github.com/haohao3k/piggery.git",
        ):
            self.assertEqual(local_dev._normalise_origin(url), local_dev.EXPECTED_ORIGIN)

    def test_origin_guard_rejects_upstream(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            with mock.patch.object(local_dev, "_git", return_value="https://github.com/sting8k/piggery.git"):
                with self.assertRaises(local_dev.LocalDevError):
                    local_dev.check_origin(Path(tmp))

    def test_non_default_home_is_rejected(self) -> None:
        with mock.patch.dict(local_dev.os.environ, {"PIGGERY_HOME": "/tmp/other-piggery"}, clear=False):
            with self.assertRaisesRegex(local_dev.LocalDevError, "unsupported"):
                local_dev.piggery_home()


class RepoScopeTests(unittest.TestCase):
    def test_rejects_a_directory_outside_the_checkout(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaisesRegex(local_dev.LocalDevError, "not the Piggery checkout"):
                local_dev.repo_root(tmp)


class GateTests(unittest.TestCase):
    def test_malformed_state_cannot_be_treated_as_idle(self) -> None:
        with self.assertRaisesRegex(local_dev.LocalDevError, "daemon state is unknown"):
            local_dev.idle_gate({}, None)
        with self.assertRaisesRegex(local_dev.LocalDevError, "pid"):
            local_dev.idle_gate({"teams": [], "solos": []}, None)

    def test_blocks_busy_team_and_unrelated_solo(self) -> None:
        snapshot = valid_snapshot(
            teams=[{"id": "review-id", "name": "review", "members": [{"id": "worker", "name": "w", "state": "working", "headless": True}]}],
            solos=[{"id": "other", "name": "other", "state": "awaiting_permission"}],
        )
        with self.assertRaisesRegex(local_dev.LocalDevError, "idle gate blocked"):
            local_dev.idle_gate(snapshot, None)

    def test_exact_caller_can_be_busy_but_team_member_cannot(self) -> None:
        snapshot = valid_snapshot(
            teams=[{"id": "review-id", "name": "review", "members": [{"id": "worker", "name": "w", "state": "working", "headless": False}]}],
            solos=[{"id": "caller-id", "name": "caller", "state": "working"}],
        )
        with self.assertRaisesRegex(local_dev.LocalDevError, "idle gate blocked"):
            local_dev.idle_gate(snapshot, "caller-id")
        allowed = valid_snapshot(solos=[{"id": "caller-id", "name": "caller", "state": "working"}])
        self.assertTrue(local_dev.idle_gate(allowed, "caller-id").running)

        gone = valid_snapshot(solos=[{"id": "gone-id", "name": "gone", "state": "gone"}])
        with self.assertRaisesRegex(local_dev.LocalDevError, "idle or working"):
            local_dev.idle_gate(gone, "gone-id")

    def test_idle_headless_is_reported_and_gone_is_allowed(self) -> None:
        snapshot = valid_snapshot(teams=[{"id": "review-id", "name": "review", "members": [
                {"id": "idle-worker", "name": "idle", "state": "idle", "headless": True},
                {"id": "gone-worker", "name": "gone", "state": "gone", "headless": True},
            ]}])
        gate = local_dev.idle_gate(snapshot, None)
        self.assertEqual(gate.headless_idle, ("review/idle (idle-worker)",))


class ReceiptTests(unittest.TestCase):
    def test_candidate_must_match_current_digest_and_sha(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            candidate, receipt_path = local_dev.candidate_paths(root)
            candidate.parent.mkdir(parents=True)
            candidate.write_bytes(b"candidate")
            candidate.chmod(0o755)
            digest = "a" * 64
            commit = "abc123"
            version = f"local-{commit}-{digest}"
            receipt_path.write_text(json.dumps({
                "schema": 1,
                "build_mode": "local",
                "source_root": str(root.resolve()),
                "state": "candidate",
                "version": version,
                "commit": commit,
                "source_digest": digest,
                "binary_sha256": local_dev._sha256_file(candidate),
            }), encoding="utf-8")
            with mock.patch.object(local_dev, "source_digest", return_value=digest), mock.patch.object(local_dev, "commit_id", return_value=commit):
                self.assertIsNotNone(local_dev._candidate_matches(root, digest, commit))
                receipt = json.loads(receipt_path.read_text())
                receipt["source_root"] = str(root.parent / "moved-checkout")
                receipt_path.write_text(json.dumps(receipt))
                self.assertIsNone(local_dev._candidate_matches(root, digest, commit))
                receipt["source_root"] = str(root.resolve())
                receipt_path.write_text(json.dumps(receipt))
                candidate.write_bytes(b"stale")
                self.assertIsNone(local_dev._candidate_matches(root, digest, commit))

    def test_pending_activation_is_not_a_successful_install(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            candidate, candidate_receipt = local_dev.candidate_paths(root)
            candidate.parent.mkdir(parents=True)
            candidate.write_bytes(b"candidate")
            candidate.chmod(0o755)
            digest, commit = "b" * 64, "abc123"
            version = f"local-{commit}-{digest}"
            sha = local_dev._sha256_file(candidate)
            candidate_receipt.write_text(json.dumps({
                "schema": 1, "build_mode": "local", "state": "candidate", "version": version,
                "source_root": str(root.resolve()),
                "commit": commit, "source_digest": digest, "binary_sha256": sha,
            }), encoding="utf-8")
            install = root / "install"
            installed = install / "piggery"
            install.mkdir(parents=True)
            with mock.patch.dict(local_dev.os.environ, {"PIGGERY_INSTALL_DIR": str(install)}, clear=False), \
                mock.patch.object(local_dev, "source_digest", return_value=digest), \
                mock.patch.object(local_dev, "commit_id", return_value=commit), \
                mock.patch.object(local_dev, "_installed_version", return_value=version), \
                mock.patch.object(local_dev, "_daemon_snapshot", return_value=valid_snapshot(version=version)):
                info = local_dev.BuildInfo(root, candidate, candidate_receipt, {
                    "schema": 1, "build_mode": "local", "state": "candidate", "version": version,
                    "source_root": str(root.resolve()),
                    "commit": commit, "source_digest": digest, "binary_sha256": sha,
                })
                _, _, pending = local_dev.install_candidate(info)
                self.assertEqual(pending["state"], "pending")
                home = root / "home"
                (home / "harness").mkdir(parents=True)
                (home / "config.yaml").write_text("{}\n", encoding="utf-8")
                failed_refresh = mock.Mock(returncode=1, stdout="", stderr="refresh failed")
                with mock.patch.object(local_dev, "piggery_home", return_value=home), \
                    mock.patch.object(local_dev, "_run", return_value=failed_refresh):
                    with self.assertRaisesRegex(local_dev.LocalDevError, "setup --refresh failed"):
                        local_dev._refresh_and_restart(root, installed, None)
                result = local_dev.inspect(root)
                activated = local_dev._activate_receipt(
                    install / local_dev.INSTALLED_RECEIPT_NAME,
                    pending,
                    valid_snapshot(version=version),
                    ("review/idle (worker-id)",),
                    valid_refresh_evidence(),
                )
                local_dev._mark_activation_failed(
                    install / local_dev.INSTALLED_RECEIPT_NAME,
                    activated,
                    "post-apply verification failed",
                )
            self.assertFalse(result.ok)
            self.assertTrue(any("activation" in failure for failure in result.failures))
            self.assertEqual(
                activated["activation"]["headless_idle"], ["review/idle (worker-id)"]
            )
            failed = json.loads(
                (install / local_dev.INSTALLED_RECEIPT_NAME).read_text(encoding="utf-8")
            )
            self.assertEqual(failed["state"], "failed")
            self.assertEqual(failed["activation"]["state"], "failed")

    def test_activation_requires_refresh_success_evidence(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "receipt.json"
            pending = {
                "schema": 1,
                "build_mode": "local",
                "state": "pending",
                "version": "local-test",
            }
            with self.assertRaisesRegex(local_dev.LocalDevError, "refresh evidence"):
                local_dev._activate_receipt(path, pending, valid_snapshot(), (), None)

    def test_installed_sha_must_equal_candidate_sha(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            candidate, candidate_receipt = local_dev.candidate_paths(root)
            candidate.parent.mkdir(parents=True)
            candidate.write_bytes(b"new")
            candidate.chmod(0o755)
            install = root / "install"
            installed = install / "piggery"
            installed.parent.mkdir(parents=True)
            installed.write_bytes(b"old")
            installed.chmod(0o755)
            digest, commit = "c" * 64, "abc123"
            version = f"local-{commit}-{digest}"
            candidate_receipt.write_text(json.dumps({
                "schema": 1, "build_mode": "local", "state": "candidate", "version": version,
                "source_root": str(root.resolve()),
                "commit": commit, "source_digest": digest, "binary_sha256": local_dev._sha256_file(candidate),
            }), encoding="utf-8")
            local_dev._write_json_atomic(install / local_dev.INSTALLED_RECEIPT_NAME, {
                "schema": 1, "build_mode": "local", "state": "activated", "version": version,
                "source_root": str(root.resolve()),
                "commit": commit, "source_digest": digest, "binary_sha256": local_dev._sha256_file(installed),
            })
            with mock.patch.dict(local_dev.os.environ, {"PIGGERY_INSTALL_DIR": str(install)}, clear=False), \
                mock.patch.object(local_dev, "source_digest", return_value=digest), \
                mock.patch.object(local_dev, "commit_id", return_value=commit), \
                mock.patch.object(local_dev, "_installed_version", return_value=version), \
                mock.patch.object(local_dev, "_daemon_snapshot", return_value=valid_snapshot(version=version)):
                result = local_dev.inspect(root)
            self.assertFalse(result.ok)
            self.assertTrue(any("SHA" in failure for failure in result.failures))


class FakeCliTests(unittest.TestCase):
    def test_daemon_read_uses_no_start_and_parses_json(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            log = root / "args"
            fake = root / "piggery"
            fake.write_text(
                "#!/bin/sh\n"
                "printf '%s\\n' \"$@\" > \"$LOCAL_DEV_TEST_LOG\"\n"
                "printf '%s\\n' '{\"pid\":1,\"started_at\":1,\"version\":\"local-test\",\"teams\":[],\"solos\":[]}'\n",
                encoding="utf-8",
            )
            fake.chmod(0o755)
            with mock.patch.dict(local_dev.os.environ, {"LOCAL_DEV_TEST_LOG": str(log)}, clear=False):
                snapshot = local_dev._daemon_snapshot(fake)
            self.assertEqual(snapshot and snapshot["version"], "local-test")
            self.assertIn("--no-start", log.read_text(encoding="utf-8").splitlines())

    def test_candidate_probe_catches_busy_daemon_when_install_is_absent(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            candidate, _ = local_dev.candidate_paths(root)
            candidate.parent.mkdir(parents=True)
            candidate.write_text(
                "#!/bin/sh\nprintf '%s\\n' '{\"pid\":1,\"started_at\":1,\"version\":\"local-test\",\"teams\":[{\"id\":\"t\",\"name\":\"team\",\"members\":[{\"id\":\"w\",\"name\":\"worker\",\"state\":\"working\",\"headless\":true}]}],\"solos\":[]}'\n",
                encoding="utf-8",
            )
            candidate.chmod(0o755)
            missing_install = root / "missing-install" / "piggery"
            with mock.patch.object(local_dev, "installed_paths", return_value=(missing_install, missing_install.with_suffix(".json"))):
                probe = local_dev._probe_binary(root)
                snapshot = local_dev._daemon_snapshot(probe)
                with self.assertRaisesRegex(local_dev.LocalDevError, "idle gate blocked"):
                    local_dev.idle_gate(snapshot, None)

    def test_late_gate_blocks_before_restart(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            home = Path(tmp) / "home"
            (home / "harness").mkdir(parents=True)
            (home / "config.yaml").write_text("{}\n", encoding="utf-8")
            busy = valid_snapshot(teams=[{"id": "t", "name": "team", "members": [{"id": "w", "name": "worker", "state": "working", "headless": True}]}])
            with mock.patch.object(local_dev, "piggery_home", return_value=home), \
                mock.patch.object(local_dev, "_probe_binary", return_value=Path("/candidate")), \
                mock.patch.object(local_dev, "_daemon_snapshot", side_effect=[busy]), \
                mock.patch.object(local_dev, "_run", return_value=mock.Mock(returncode=0, stdout="", stderr="")) as run:
                with self.assertRaisesRegex(local_dev.LocalDevError, "idle gate blocked"):
                    local_dev._refresh_and_restart(root, Path("/candidate"), None)
            self.assertEqual(run.call_count, 1)

    def test_restart_uses_admin_auth_even_when_participant_env_is_present(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            home = root / "home"
            (home / "harness").mkdir(parents=True)
            (home / "config.yaml").write_text("{}\n", encoding="utf-8")
            idle = valid_snapshot()
            binary = Path("/candidate")
            ok = mock.Mock(returncode=0, stdout="", stderr="")
            with mock.patch.dict(local_dev.os.environ, {"PIGGERY_ID": "caller", "PIGGERY_TOKEN": "secret"}, clear=False), \
                mock.patch.object(local_dev, "piggery_home", return_value=home), \
                mock.patch.object(local_dev, "_probe_binary", return_value=binary), \
                mock.patch.object(local_dev, "_daemon_snapshot", side_effect=[idle, idle]), \
                mock.patch.object(local_dev, "_run", return_value=ok) as run:
                snapshot, headless_idle, refresh_evidence = local_dev._refresh_and_restart(root, binary, None)
            self.assertEqual(snapshot, idle)
            self.assertEqual(headless_idle, ())
            self.assertTrue(local_dev._refresh_evidence_ok(refresh_evidence))
            self.assertEqual(run.call_args_list[0].args[0], [binary, "setup", "--refresh"])
            self.assertEqual(run.call_args_list[1].args[0], [binary, "--admin", "restart"])


class LockTests(unittest.TestCase):
    def test_concurrent_apply_lock_is_refused(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            with mock.patch.dict(local_dev.os.environ, {"PIGGERY_INSTALL_DIR": tmp}, clear=False):
                lock = Path(tmp) / local_dev.INSTALL_LOCK_NAME
                lock.parent.mkdir(parents=True, exist_ok=True)
                with lock.open("a+") as held:
                    import fcntl
                    fcntl.flock(held.fileno(), fcntl.LOCK_EX | fcntl.LOCK_NB)
                    with self.assertRaisesRegex(local_dev.LocalDevError, "another local-dev apply"):
                        with local_dev.install_lock():
                            pass


if __name__ == "__main__":
    unittest.main()
