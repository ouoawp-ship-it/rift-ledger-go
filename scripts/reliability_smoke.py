#!/usr/bin/env python3
"""Real HTTP + process restart acceptance in temporary player-only data; no Telegram."""
import concurrent.futures
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]

def main():
    checks = []
    with tempfile.TemporaryDirectory(prefix="rift-reliability-") as folder:
        tmp = Path(folder)
        binary = tmp / "server"
        subprocess.run(["go", "build", "-o", str(binary), "./cmd/server"], cwd=ROOT, check=True)
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        token = secrets.token_hex(32)
        env = {k: v for k, v in os.environ.items() if not k.startswith("TG_")}
        env.update(ADMIN_TOKEN=token, LISTEN_ADDR=f"127.0.0.1:{port}", DATA_DIR=str(tmp / "data"))
        heroes = [{"id": x, "name": x} for x in ["Garen", "Ashe", "Lux", "Jinx", "Annie"]]
        cache = tmp / "data" / "champions"
        cache.mkdir(parents=True)
        (cache / "champions.json").write_text(json.dumps({"version":"1.0.0", "champions":heroes}))
        def api(path, body=None, key=None):
            headers = {"Authorization":"Bearer " + token}
            data = None
            if body is not None:
                headers.update({"Content-Type":"application/json", "Idempotency-Key":key or str(uuid.uuid4())})
                data = json.dumps(body).encode()
            req = urllib.request.Request(f"http://127.0.0.1:{port}/api/" + path, data=data, headers=headers)
            with urllib.request.urlopen(req, timeout=10) as res:
                assert res.headers.get("X-Request-ID")
                payload = json.load(res)
                assert payload["ok"], payload
                return payload["data"]
        def start(log):
            proc = subprocess.Popen([str(binary)], env=env, stdout=log, stderr=log)
            for _ in range(100):
                if proc.poll() is not None:
                    raise AssertionError("isolated server exited before readiness")
                try:
                    api("state")
                    return proc
                except (OSError, urllib.error.URLError):
                    time.sleep(.05)
            proc.terminate()
            proc.wait(timeout=10)
            raise AssertionError("readiness timeout")
        with (tmp / "server.log").open("wb") as log:
            proc = start(log)
            try:
                state = api("state")
                rules = state["rules"] | {"payout":[1.2]*11, "confirmed":True}
                api("rules", {"expected_version":state["rules_version"], "rules":rules})
                player = api("accounts", {"telegram_id":123456789, "name":"isolated player", "enabled":True})
                credit = {"account_id":player["id"], "delta":"1000.199", "note":"isolated acceptance"}
                first = api("adjustments", credit, "test-credit-replay")
                assert first == api("adjustments", credit, "test-credit-replay")
                r = api("rounds", {"number":"2026-09-21-0001", "banker":1, "heroes":heroes})
                path = "rounds/" + r["id"]
                api(path + "/open", {})
                api("bets", {"account_id":player["id"], "round_id":r["id"], "position":2, "stake":"100.001"})
                api(path + "/close", {})
                settle = {"duration_seconds":1200, "damages":["12710", "12745", "99999", "12737", "12345"]}
                preview = api(path + "/preview", settle)
                settle["preview_token"] = preview["token"]
                result = api(path + "/settle", settle, "test-settlement-replay")
                assert result == api(path + "/settle", settle, "test-settlement-replay")
                account = next(a for a in api("state")["accounts"] if a["id"] == player["id"])
                assert result["lines"][0]["game_delta"] == 120.001, result
                assert account["balance"] == 1120.200 and account["locked"] == 0, account
                rows = api("accounts")["rows"]
                assert rows[0]["balance"] == "1120.200", rows
                entries = api("entries?account_id=" + player["id"])
                assert entries[0]["delta"] == "120.001" and entries[0]["balance_after"] == "1120.200", entries
                assert api("reconcile")["balanced"]
                checks.append("fractional credit 1000.199 + bet 100.001 × 1.2 = balance 1120.200; duplicate credit/settlement, summary, ledger and reconciliation")
                with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
                    assert all(pool.map(lambda _:bool(api("state")), range(32)))
                checks.append("32 HTTP reads with 8 concurrent clients returned complete JSON and request IDs")
                saved = api("bot-settings")
                patch = {"token":"", "bot_username":"", "group_id":0, "admin_id":0, "support_username":"support_demo", "enabled":False, "revision":saved["revision"]}
                reply = api("bot-settings", patch)
                assert reply["support_username"] == "support_demo"
                assert proc.wait(timeout=15) == 0
                proc = start(log)
                active = api("bot-settings")
                assert active["support_username"] == "support_demo" and not active["restart_required"]
                assert api("bot-settings", patch)["revision"] == reply["revision"]
                time.sleep(.3)
                assert proc.poll() is None, "replayed save restarted unchanged configuration"
                account = next(a for a in api("state")["accounts"] if a["id"] == player["id"])
                assert account["balance"] == 1120.200 and api("reconcile")["balanced"]
                checks.append("save response before graceful exit; restart persists settings and ledger; replay does not restart")
            finally:
                if proc.poll() is None:
                    proc.terminate()
                    proc.wait(timeout=20)
    report = {"passed":True, "real_telegram":False, "production_data":False, "checks":checks}
    print(json.dumps(report, ensure_ascii=False, indent=2))

if __name__ == "__main__":
    main()
