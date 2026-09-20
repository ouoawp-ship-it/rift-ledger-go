#!/usr/bin/env python3
"""Build and test the actual server in a disposable database. No real Telegram calls.
Requirements: Linux + Go + gcc + SQLite development headers + Python 3 standard library.
This script never loads .env or connects to an existing server/database.
"""
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
    report = {"real_telegram": False, "isolated_database": True, "checks": []}
    with tempfile.TemporaryDirectory(prefix="rift-go-smoke-") as tmp:
        tmp = Path(tmp)
        binary = tmp / "server"
        subprocess.run(["go", "build", "-trimpath", "-o", str(binary), "./cmd/server"], cwd=ROOT, check=True)
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        base = f"http://127.0.0.1:{port}"
        secret = secrets.token_hex(32)
        env = {k: v for k, v in os.environ.items() if not k.startswith("TG_")}
        env.update(ADMIN_TOKEN=secret, LISTEN_ADDR=f"127.0.0.1:{port}", DATA_DIR=str(tmp / "data"))
        def request(path, body=None, key=None, status=200, auth=True):
            headers = {"Authorization": "Bearer " + secret} if auth else {}
            if body is not None:
                headers.update({"Content-Type": "application/json", "Idempotency-Key": key or str(uuid.uuid4())})
            req = urllib.request.Request(base + path, headers=headers, data=None if body is None else json.dumps(body).encode())
            try:
                response = urllib.request.urlopen(req, timeout=10)
            except urllib.error.HTTPError as err:
                response = err
            with response:
                assert response.status == status, (path, response.status, response.read())
                content = response.read()
                if path == "/" or path.endswith((".js", ".css")):
                    return content
                value = json.loads(content)
            return value.get("data", value)
        def api(path, body=None, **kwargs):
            return request("/api/" + path, body, **kwargs)
        def start(logfile):
            proc = subprocess.Popen([str(binary)], env=env, stdout=logfile, stderr=subprocess.STDOUT)
            for _ in range(150):
                if proc.poll() is not None:
                    raise RuntimeError((tmp / "server.log").read_text())
                try:
                    request("/healthz", auth=False)
                    return proc
                except OSError:
                    time.sleep(0.1)
            proc.kill()
            proc.wait()
            raise RuntimeError("Server readiness timeout")
        def stop(proc):
            proc.terminate()
            try:
                proc.wait(timeout=25)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()
                raise RuntimeError("Graceful shutdown timed out")
            assert proc.returncode == 0
        with (tmp / "server.log").open("w") as logfile:
            proc = start(logfile)
            try:
                api("state", auth=False, status=401)
                assert "峡谷账房" in request("/", auth=False).decode()
                assert b"strict" in request("/app.js", auth=False)
                request("/style.css", auth=False)
                initial = api("state")
                assert not initial["rules"]["confirmed"]
                calc = api("calculate", {"damage": "12745", "banker_damage": "12710"})
                assert calc["hand"]["rank"] == 9 and calc["banker"]["rank"] == 1
                assert calc["comparison"]["outcome"] == "WIN"
                report["checks"].append("Real HTTP health/auth/assets/calculator, unconfirmed initial rules")
                heroes = [{"id": "H" + str(i), "name": "测试敌方英雄" + str(i)} for i in range(1, 6)]
                first = api("rounds", {"number": "2026-09-20-0001", "banker": 3, "heroes": heroes})
                rid = first["id"]
                api(f"rounds/{rid}/open", {}, status=409)
                rules = initial["rules"] | {"confirmed": True, "fee_timing": "settlement", "void_fee": "refund", "fee_recipient": "fees"}
                api("rules", {"expected_version": initial["rules_version"], "rules": rules})
                player = api("accounts", {"telegram_id": 123456789, "name": "隔离测试玩家", "enabled": True})
                assert player["balance"] == 0
                api("adjustments", {"account_id": "house", "delta": 10000, "note": "隔离测试"})
                adj = {"account_id": player["id"], "delta": 1000, "note": "隔离测试"}
                assert api("adjustments", adj, key="smoke-player-adjust") == api("adjustments", adj, key="smoke-player-adjust")
                api("adjustments", adj | {"delta": 1}, key="smoke-player-adjust", status=409)
                api(f"rounds/{rid}/open", {})
                api(f"rounds/{rid}/configure", {"number": first["number"], "banker": 4, "heroes": heroes}, status=409)
                bet = {"account_id": player["id"], "round_id": rid, "position": 1, "stake": 100}
                assert api("bets", bet, key="smoke-test-bet") == api("bets", bet, key="smoke-test-bet")
                accounts = {a["id"]: a for a in api("accounts")}
                assert accounts[player["id"]]["locked"] == 101 and accounts["house"]["locked"] == 400
                assert len(api("bets?account_id=" + player["id"])) == 1
                report["checks"].append("Explicit rules, whitelist no gifts, manual banker3, duplicate adjustment/bet idempotency, correct freezes")
                api(f"rounds/{rid}/close", {})
                api("bets", bet, status=409)
                inp = {"duration_seconds": 1200, "damages": ["12745", "99999", "12710", "12737", "12345"]}
                preview = api(f"rounds/{rid}/preview", inp)
                api(f"rounds/{rid}/settle", inp | {"duration_seconds": 1201, "preview_token": preview["token"]}, status=409)
                settlement = inp | {"preview_token": preview["token"]}
                done = api(f"rounds/{rid}/settle", settlement)
                entries_before = len(api("entries"))
                again = api(f"rounds/{rid}/settle", settlement)
                assert done == again and len(api("entries")) == entries_before
                accounts = {a["id"]: a for a in api("accounts")}
                balances = {a: accounts[a]["balance"] for a in [player["id"], "house", "fees"]}
                assert balances == {player["id"]: 1299, "house": 9700, "fees": 1}, balances
                assert all(a["locked"] == 0 for a in accounts.values())
                state = api("state")
                assert state["active_round"]["number"] == "2026-09-20-0002"
                assert state["active_round"]["state"] == "DRAFT" and state["active_round"]["banker"] == 0
                assert state["active_round"]["heroes"] == []
                reconcile = api("reconcile")
                assert reconcile["balanced"] and reconcile["issues"] == []
                report["checks"].append("Closed rejects bets; changed preview rejects settlement; repeat settlement has no additional entries")
                report["checks"].append("Final player1299, house9700, fees1; zero frozen; next empty draft; all accounts/entries reconcile")
                # Process lock: second instance must not open this database or HTTP listener.
                duplicate = subprocess.run([str(binary)], env=env, capture_output=True, timeout=5)
                assert duplicate.returncode != 0 and "已有服务进程" in duplicate.stderr.decode()
                stop(proc)
                proc = start(logfile)
                assert api("rounds/" + rid)["result"] == done
                assert api("reconcile")["balanced"]
                assert api("state")["active_round"]["number"] == "2026-09-20-0002"
                report["checks"].append("Second-instance lock; graceful SIGTERM; restart preserves results, next draft and balanced ledger")
                report["final_balances"] = balances
                report["database_version"] = state["sqlite_version"]
            finally:
                if proc.poll() is None:
                    stop(proc)
    report["passed"] = True
    report["go_version"] = subprocess.check_output(["go", "version"], text=True).strip()
    text = json.dumps(report, ensure_ascii=False, indent=2)
    output = ROOT / "docs" / "test-artifacts" / "http-smoke.json"
    output.parent.mkdir(parents=True, exist_ok=True)
    output.write_text(text + "\n", encoding="utf-8")
    print(text)

if __name__ == "__main__":
    main()
