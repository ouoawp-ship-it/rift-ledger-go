#!/usr/bin/env python3
"""Restore a self-created live SQLite backup into an isolated, Telegram-disabled service.
No production URL, data path or credentials accepted. Requires Linux Go/CGO + Python.
"""
import hashlib
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import sqlite3
import subprocess
import tempfile
import time
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]


def main():
    with tempfile.TemporaryDirectory(prefix="rift-restore-drill-") as folder:
        tmp = Path(folder)
        server, checker = tmp / "server", tmp / "dbcheck"
        for binary, package in [(server, "./cmd/server"), (checker, "./cmd/dbcheck")]:
            subprocess.run(["go", "build", "-o", str(binary), package], cwd=ROOT, check=True)
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        token = secrets.token_hex(32)
        env = {k: v for k, v in os.environ.items() if not k.startswith("TG_")}
        env.update(ADMIN_TOKEN=token, LISTEN_ADDR=f"127.0.0.1:{port}")
        heroes = [{"id": h, "name": h} for h in ["Garen", "Ashe", "Lux", "Jinx", "Annie"]]

        def api(path, body=None, key=None):
            headers = {"Authorization": "Bearer " + token}
            data = None
            if body is not None:
                headers.update({"Content-Type": "application/json", "Idempotency-Key": key or str(uuid.uuid4())})
                data = json.dumps(body).encode()
            request = urllib.request.Request(f"http://127.0.0.1:{port}/api/" + path, data=data, headers=headers)
            with urllib.request.urlopen(request, timeout=20) as response:
                result = json.load(response)
                assert result["ok"], result
                return result["data"]

        def start(data, log):
            cache = data / "champions"
            cache.mkdir(parents=True, exist_ok=True)
            (cache / "champions.json").write_text(json.dumps({"version": "1.0.0", "champions": heroes}))
            # Fresh directory: runtime-settings.json with real credentials is never copied.
            assert not (data / "runtime-settings.json").exists()
            process = subprocess.Popen([str(server)], env=env | {"DATA_DIR": str(data)}, stdout=log, stderr=log)
            try:
                for _ in range(150):
                    if process.poll() is not None:
                        raise RuntimeError("isolated service exited")
                    try:
                        api("state")
                        return process
                    except OSError:
                        time.sleep(.05)
                raise RuntimeError("isolated service readiness timeout")
            except BaseException:
                stop(process)
                raise

        def stop(process):
            if process and process.poll() is None:
                process.terminate()
                process.wait(timeout=20)

        source, restored = tmp / "source", tmp / "restored"
        process = None
        with (tmp / "service.log").open("wb") as log:
            try:
                process = start(source, log)
                state = api("state")
                api("rules", {"expected_version": state["rules_version"], "rules": state["rules"] | {"confirmed": True, "payout": [1.2] * 11}})
                account = api("accounts", {"telegram_id": 123456789, "name": "restore-test", "enabled": True})
                credit = {"account_id": account["id"], "delta": "1000.199", "note": "isolated restore credit"}
                credit_response = api("adjustments", credit, "restore-credit-0001")
                round_ = api("rounds", {"number": "2026-09-22-0001", "banker": 1, "heroes": heroes})
                path = "rounds/" + round_["id"]
                api(path + "/open", {})
                bet = {"round_id": round_["id"], "account_id": account["id"], "position": 2, "stake": "100.001"}
                api("bets", bet)
                api(path + "/close", {})
                settlement = {"damages": ["12710", "12737", "12710", "12710", "12710"]}
                settlement["preview_token"] = api(path + "/preview", settlement)["token"]
                result = api(path + "/settle", settlement, "restore-settle-0001")
                active = api("state")["active_round"]
                api("rounds/" + active["id"] + "/configure", {"number": active["number"], "banker": 1, "heroes": heroes})
                api("rounds/" + active["id"] + "/open", {})
                api("bets", bet | {"round_id": active["id"]}, "restore-reserved-bet")
                template = next(t for t in api("message-templates") if t["id"] == "round_close")
                api("message-templates", {"id": template["id"], "revision": template["revision"], "blocks": [{"type": "text", "text": "恢复验收 {当前期数}"}]})
                with sqlite3.connect(source / "rift-ledger.db") as db:
                    db.execute("INSERT INTO meta(key,value) VALUES('tg_offset','12345')")
                    db.execute("INSERT INTO outbox(key,chat_id,payload,state,created_at) VALUES('ambiguous',123456789,'{}','INFLIGHT',1)")
                baseline = {"state": api("state"), "ledger": api("reconcile"), "templates": api("message-templates")}
                backup = tmp / "consistent.db"
                with sqlite3.connect(source / "rift-ledger.db") as src, sqlite3.connect(backup) as dest:
                    src.backup(dest)
                digest = hashlib.sha256(backup.read_bytes()).hexdigest()
                checked = json.loads(subprocess.check_output([str(checker), "-db", str(backup)], text=True))
                assert checked["ok"] and checked["ledger_chain_issues"] == 0 and checked["daily_total_issues"] == 0
                assert digest == hashlib.sha256(backup.read_bytes()).hexdigest(), "checker changed source backup"
                # Prove later changes are not mistaken for the snapshot's state.
                api("adjustments", credit | {"delta": "0.001"}, "after-snapshot-credit")
                stop(process)
                restored.mkdir()
                shutil.copy2(backup, restored / "rift-ledger.db")
                process = start(restored, log)
                current = api("state")
                assert current["accounts"] == baseline["state"]["accounts"]
                assert current["rules"] == baseline["state"]["rules"]
                assert current["active_round"] == baseline["state"]["active_round"]
                assert api("reconcile") == baseline["ledger"]
                assert api("message-templates") == baseline["templates"]
                assert api("adjustments", credit, "restore-credit-0001") == credit_response
                assert api(path + "/settle", settlement, "restore-settle-0001") == result
                assert api("state")["accounts"] == baseline["state"]["accounts"]
                with sqlite3.connect(restored / "rift-ledger.db") as db:
                    assert db.execute("SELECT value FROM meta WHERE key='tg_offset'").fetchone()[0] == "12345"
                    assert db.execute("SELECT state,attempts FROM outbox WHERE key='ambiguous'").fetchone() == ("UNKNOWN", 0)
                    assert db.execute("SELECT COUNT(*) FROM outbox WHERE attempts!=0").fetchone()[0] == 0
                assert api("operations")["receiver"]["receiver_state"] == "disabled"
                print(json.dumps({"passed": True, "real_telegram": False, "production_data": False, "offline_verification": checked, "checks": ["online backup copied into fresh data directory", "balance 1120.200 and reserved 100.001 retained", "rules, templates, active round, ledger and offset retained", "credit and settlement replay do not change restored balances", "pending messages not sent; interrupted send becomes UNKNOWN", "post-backup credit correctly excluded; original backup unchanged"]}, ensure_ascii=False, indent=2))
            finally:
                stop(process)


if __name__ == "__main__":
    main()
