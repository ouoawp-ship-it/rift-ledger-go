#!/usr/bin/env python3
"""Fault injection for recovery orchestration; no real Docker, network or credentials."""
import json
import os
from pathlib import Path
import sqlite3
import shutil
import subprocess
import sys
import tempfile
import time
import unittest
from unittest.mock import patch

import recovery as r


class FakeDocker:
    def __init__(self):
        self.info = {"container": "a" * 64, "started_at": "fixed-start", "image": "sha256:" + "b" * 64,
                     "environment": ["ADMIN_TOKEN=test-secret", "TG_BOT_TOKEN=test-secret"]}
        self.settings = b'{"token":"test-secret", "enabled":true}'
        self.fail = False
        self.restart = False

    def identity(self):
        return self.info.copy()

    def capacity(self, cid, store):
        pass

    def optional(self, cid, name):
        return self.settings if name.endswith("runtime-settings.json") else None

    def snapshot(self, cid, target):
        if self.fail:
            raise OSError("simulated disk full")
        with sqlite3.connect(target) as db:
            db.execute("CREATE TABLE sentinel(value INTEGER)")
            db.execute("INSERT INTO sentinel VALUES(6427323)")
        if self.restart:
            self.info["started_at"] = "changed-start"

    def inspect(self, image, database):
        with sqlite3.connect(database) as db:
            assert db.execute("PRAGMA integrity_check").fetchone()[0] == "ok"
        return {"ok": True, "schema_version": "6"}


class RecoveryTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        self.root = Path(self.tmp.name)
        self.store = self.root / "backups" / "recovery"
        (self.root / ".env").write_text("ADMIN_TOKEN=test-secret\n")
        (self.root / "compose.yaml").write_text("services: {}\n")
        self.docker = FakeDocker()
        self.git = patch.object(r, "run", return_value=b"c" * 40)
        self.git.start()
        self.addCleanup(self.git.stop)

    def backup(self, keep=96):
        return r.backup(self.root, self.store, keep, self.docker)

    def test_complete_bundle_and_private_permissions(self):
        bundle = self.backup()
        manifest, report = r.verify(bundle, self.docker)
        self.assertTrue(report["ok"])
        self.assertIn("container-environment.json", manifest["files"])
        self.assertEqual(bundle.stat().st_mode & 0o777, 0o700)
        for path in bundle.iterdir():
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)
        self.assertTrue(r.health(self.store, 3600)["ok"])

    def test_failed_backup_preserves_previous_and_no_partial_publish(self):
        bundle = self.backup()
        before = r.digest(bundle / "ledger.db")
        self.docker.fail = True
        with self.assertRaises(OSError):
            self.backup()
        self.assertEqual(r.digest(bundle / "ledger.db"), before)
        self.assertEqual(list(self.store.glob("bundle-*")), [bundle])
        self.assertEqual(list(self.store.glob(".partial-*")), [])
        with self.assertRaises(r.RecoveryError):
            r.health(self.store, 3600)

    def test_configuration_change_refuses_publish(self):
        def changed(cid, target):
            FakeDocker.snapshot(self.docker, cid, target)
            self.docker.settings = b'{"enabled":false}'
        with patch.object(self.docker, "snapshot", side_effect=changed):
            with self.assertRaisesRegex(r.RecoveryError, "发生变化"):
                self.backup()
        self.assertEqual(list(self.store.glob("bundle-*")), [])

    def test_restart_during_backup_refuses_publish(self):
        self.docker.restart = True
        with self.assertRaisesRegex(r.RecoveryError, "发生变化"):
            self.backup()

    def test_bad_inspection_preserves_old_bundle(self):
        previous = self.backup()
        with patch.object(self.docker, "inspect", side_effect=r.RecoveryError("bad ledger")):
            with self.assertRaises(r.RecoveryError):
                self.backup()
        self.assertEqual(list(self.store.glob("bundle-*")), [previous])

    def test_low_space_stops_before_snapshot_and_retention(self):
        previous = self.backup()
        with patch.object(self.docker, "capacity", side_effect=r.RecoveryError("low space")), patch.object(self.docker, "snapshot") as snapshot:
            with self.assertRaises(r.RecoveryError):
                self.backup()
            snapshot.assert_not_called()
        self.assertTrue(previous.exists())

    def test_tamper_cannot_prepare_or_pass_status(self):
        bundle = self.backup()
        (bundle / "ledger.db").write_bytes(b"corrupt")
        with self.assertRaises(r.RecoveryError):
            r.prepare(bundle, self.root / "restored", self.docker)
        self.assertFalse((self.root / "restored").exists())
        with self.assertRaises(r.RecoveryError):
            r.health(self.store, 3600)

    def test_symlink_and_path_traversal_rejected(self):
        bundle = self.backup()
        manifest = json.loads((bundle / "manifest.json").read_bytes())
        (bundle / "environment.env").unlink()
        (bundle / "environment.env").symlink_to(self.root / ".env")
        with self.assertRaises(r.RecoveryError):
            r.verify_files(bundle)
        manifest["files"]["../.env"] = manifest["files"]["environment.env"]
        (bundle / "manifest.json").write_text(json.dumps(manifest))
        with self.assertRaises(r.RecoveryError):
            r.verify_files(bundle)

    def test_prepare_disables_bot_and_does_not_copy_secrets(self):
        bundle = self.backup()
        original = r.digest(bundle / "ledger.db")
        destination = r.prepare(bundle, self.root / "restored", self.docker)
        settings = json.loads((destination / "data/runtime-settings.json").read_bytes())
        self.assertFalse(settings["enabled"])
        self.assertEqual(settings["token"], "")
        compose = json.loads((destination / "compose.yaml").read_bytes())["services"]["review"]
        self.assertEqual(compose["network_mode"], "none")
        self.assertNotIn("ports", compose)
        self.assertNotIn("test-secret", (destination / ".env").read_text())
        self.assertFalse((destination / "container-environment.json").exists())
        self.assertEqual(r.digest(destination / "data/rift-ledger.db"), original)
        self.assertEqual(r.digest(bundle / "ledger.db"), original)

    def test_prepare_never_overwrites_target(self):
        bundle = self.backup()
        target = self.root / "live"
        target.mkdir()
        (target / "sentinel").write_text("keep me")
        with self.assertRaises(r.RecoveryError):
            r.prepare(bundle, target, self.docker)
        self.assertEqual((target / "sentinel").read_text(), "keep me")

    def test_retention_only_removes_owned_valid_bundles(self):
        self.backup()
        old = self.root / "backups/legacy.db"
        old.write_bytes(b"keep")
        unknown = self.store / "bundle-user-copy"
        unknown.mkdir()
        corrupt = self.store / ("bundle-20000101T000000Z-" + "e" * 32)
        corrupt.mkdir()
        for _ in range(4):
            self.backup(keep=3)
            self.assertTrue(r.health(self.store, 3600)["ok"])
        valid = [p for p in self.store.iterdir() if r.NAME.fullmatch(p.name) and p != corrupt]
        self.assertEqual(len(valid), 3)
        self.assertTrue(old.exists() and unknown.exists() and corrupt.exists())

    def test_minimum_retention_and_lock(self):
        with self.assertRaises(r.RecoveryError):
            self.backup(keep=2)
        with r.locked(self.store):
            with self.assertRaisesRegex(r.RecoveryError, "正在执行"):
                self.backup()

    def test_expired_snapshot_not_hidden_by_fresh_completion(self):
        bundle = self.backup()
        manifest = json.loads((bundle / "manifest.json").read_bytes())
        manifest["started_at"] = time.time() - 7200
        (bundle / "manifest.json").write_text(json.dumps(manifest))
        with self.assertRaisesRegex(r.RecoveryError, "过期"):
            r.health(self.store, 3600)

    def test_command_errors_do_not_leak_captured_secrets(self):
        self.git.stop()
        error = r.subprocess.CalledProcessError(1, "docker", output=b"test-secret", stderr=b"test-secret")
        with patch.object(r.subprocess, "run", side_effect=error):
            with self.assertRaises(r.RecoveryError) as caught:
                r.run(["docker", "inspect", "x"])
        self.assertNotIn("test-secret", str(caught.exception))

    def test_docker_checker_has_no_network_or_live_mount(self):
        docker = r.Docker(self.root)
        with patch.object(docker, "command", return_value=b'{"ok":true}') as command:
            docker.inspect(self.docker.info["image"], self.root / "snapshot.db")
        args = command.call_args.args
        self.assertEqual(args[args.index("--network") + 1], "none")
        self.assertIn("--read-only", args)
        self.assertIn("readonly", args[args.index("--mount") + 1])
        self.assertEqual(args[args.index("--entrypoint") + 1], "/usr/local/bin/rift-dbcheck")
        self.assertNotIn("--env-file", args)

    def test_docker_capacity_checks_data_volume_space(self):
        docker = r.Docker(self.root)
        with patch.object(docker, "command", side_effect=[b'100000', b'Filesystem 1024-blocks Used Available Capacity Mounted\n/dev/test 1000 999 1 99% /data\n']):
            with self.assertRaisesRegex(r.RecoveryError, "磁盘余量不足"):
                docker.capacity("a" * 64, self.root)

    @unittest.skipUnless(shutil.which("systemd-analyze"), "systemd unavailable")
    def test_generated_systemd_units_parse_with_spaces(self):
        source = (r.ROOT / "scripts/install-recovery-timer.sh").read_text().split("<<'PY'\n", 1)[1].split("\nPY\n", 1)[0]
        units = self.root / "units"
        units.mkdir()
        source = source.replace("Path('/etc/systemd/system')", "Path(" + repr(str(units)) + ")")
        with patch.object(sys, "argv", ["generator", "/opt/rift ledger with spaces"]):
            exec(source, {"__name__": "__main__"})
        # Supply a harmless stub dependency for test machines without Docker's
        # service installed; no units are installed or started by analyze.
        (units / "docker.service").write_text("[Service]\nExecStart=/bin/true\n")
        result = subprocess.run(["systemd-analyze", "verify", str(units / "rift-ledger-backup.service"), str(units / "rift-ledger-backup.timer")],
                                capture_output=True, text=True, env=os.environ | {"SYSTEMD_UNIT_PATH": str(units) + ":"})
        self.assertEqual(result.returncode, 0, result.stderr)


if __name__ == "__main__":
    unittest.main(verbosity=2)
