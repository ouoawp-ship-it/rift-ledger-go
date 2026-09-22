#!/usr/bin/env python3
"""Isolated real-HTTP concurrency acceptance. Always creates and removes its own DB.
No production URL/token arguments and Telegram environment is cleared deliberately.
"""
import concurrent.futures
import argparse
import json
import math
import os
import random
from pathlib import Path
import secrets
import socket
import statistics
import sqlite3
import subprocess
import tempfile
import time
import urllib.request

ROOT = Path(__file__).resolve().parents[1]

def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--history-entries", type=int, default=0, help="isolated synthetic GAME ledger rows (0..1000000)")
    args = parser.parse_args()
    if not 0 <= args.history_entries <= 1000000:
        parser.error("--history-entries must be 0..1000000")
    with tempfile.TemporaryDirectory(prefix="rift-load-") as folder:
        tmp = Path(folder)
        binary = tmp / "server"
        subprocess.run(["go", "build", "-o", str(binary), "./cmd/server"], cwd=ROOT, check=True)
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        token = secrets.token_hex(32)
        env = {k:v for k,v in os.environ.items() if not k.startswith("TG_")}
        env.update(ADMIN_TOKEN=token, LISTEN_ADDR=f"127.0.0.1:{port}", DATA_DIR=str(tmp / "data"))
        def api(path, body=None, key=""):
            headers = {"Authorization":"Bearer " + token}
            data = None
            if body is not None:
                headers.update({"Content-Type":"application/json", "Idempotency-Key":key})
                data = json.dumps(body).encode()
            req = urllib.request.Request(f"http://127.0.0.1:{port}/api/"+path, data=data, headers=headers)
            with urllib.request.urlopen(req, timeout=20) as res:
                assert res.headers.get("X-Request-ID")
                payload = json.load(res)
                assert payload["ok"], payload
                return payload["data"]
        with (tmp / "server.log").open("wb") as log:
            proc = subprocess.Popen([str(binary)], env=env, stdout=log, stderr=log)
            try:
                for _ in range(100):
                    if proc.poll() is not None:
                        raise AssertionError("server exited before readiness")
                    try:
                        api("state")
                        break
                    except OSError:
                        time.sleep(.05)
                else:
                    raise AssertionError("server not ready")
                ids = []
                for i in range(16):
                    account=api("accounts", {"telegram_id":10000+i,"name":f"load-{i}","enabled":True}, f"load-create-{i}")
                    ids.append(account["id"])
                    api("adjustments", {"account_id":ids[-1],"delta":"1000.000","note":"isolated seed"}, f"load-seed-{i}")
                # Seed only this newly-created temporary database. Each synthetic
                # GAME adds exactly 0.001, with a correct running balance and
                # transactional daily-summary trigger; never reads a live path.
                extra = [0] * 16
                if args.history_entries:
                    with sqlite3.connect(tmp / "data" / "rift-ledger.db") as db:
                        def entries():
                            for i in range(args.history_entries):
                                p = i % 16
                                extra[p] += 1
                                yield (f"scale-game-{i}", ids[p], 1000000 + extra[p], int(time.time()))
                        db.executemany("INSERT INTO entries(batch,account_id,kind,delta,balance_after,note,created_at) VALUES(?,?,'GAME',1,?,'isolated scale seed',?)", entries())
                        db.executemany("UPDATE accounts SET balance=balance+? WHERE id=?", zip(extra,ids))
                expected = {ids[i]: f"{(1004004 + extra[i]) // 1000}.{(1004004 + extra[i]) % 1000:03d}" for i in range(16)}
                def job(i):
                    start=time.perf_counter()
                    if i<64:
                        body={"account_id":ids[i%16],"delta":"1.001","note":"isolated concurrent credit"}
                        first=api("adjustments",body,f"load-credit-{i}")
                        assert first==api("adjustments",body,f"load-credit-{i}")
                        kind="credit_and_replay"
                    else:
                        paths=["accounts","state","operations","reconcile" if args.history_entries else "bot-connection"]
                        path=paths[(i-64)%4]
                        api(path)
                        kind=path
                    return kind,(time.perf_counter()-start)*1000
                jobs=list(range(224))
                random.Random(20260922).shuffle(jobs)
                start=time.perf_counter()
                with concurrent.futures.ThreadPoolExecutor(max_workers=8) as pool:
                    results=list(pool.map(job,jobs))
                elapsed=time.perf_counter()-start
                accounts=api("accounts")["rows"]
                assert len(accounts)==16
                for a in accounts:
                    assert a["balance"]==expected[a["id"]],a
                with sqlite3.connect(tmp / "data" / "rift-ledger.db") as db:
                    assert db.execute("SELECT COALESCE(SUM(delta),0) FROM daily_game_totals").fetchone()[0] == args.history_entries
                    assert db.execute("SELECT COALESCE(SUM(delta),0) FROM entries WHERE kind='GAME'").fetchone()[0] == args.history_entries
                assert api("reconcile")["balanced"]
                ops=api("operations")
                assert ops["receiver"]["receiver_state"]=="disabled"
                groups={}
                for kind,ms in results:
                    groups.setdefault(kind,[]).append(ms)
                report={"passed":True,"production_data":False,"real_telegram":False,"history_entries":args.history_entries,"concurrency":8,"http_requests":288,"elapsed_seconds":round(elapsed,3),"requests_per_second":round(288/elapsed,2),"latency_ms":{},"database":ops["database"],"audit_database":ops.get("audit_database"),"checks":["64 unique credits replayed once each; no duplicate credits","all 16 balances match integer oracle including seeded history","daily summaries match authoritative GAME entries","ledger reconciliation balanced","monitoring/statistics queries mixed with writes; scale mode includes 40 full reconciliations"]}
                for kind,values in groups.items():
                    values.sort()
                    report["latency_ms"][kind]={"samples":len(values),"p50":round(statistics.median(values),2),"p95":round(values[math.ceil(len(values)*.95)-1],2),"max":round(max(values),2)}
                print(json.dumps(report,ensure_ascii=False,indent=2))
            finally:
                if proc.poll() is None:
                    proc.terminate()
                    proc.wait(timeout=20)

if __name__ == "__main__":
    main()
