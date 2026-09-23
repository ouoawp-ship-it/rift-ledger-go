#!/usr/bin/env python3
"""Isolated acceptance for player balance filters; never uses production credentials."""
import json, os, secrets, socket, sqlite3, subprocess, tempfile, time, urllib.request, uuid
from pathlib import Path
ROOT=Path(__file__).resolve().parents[1]
with tempfile.TemporaryDirectory(prefix="rift-player-filter-") as folder:
    tmp=Path(folder); token=secrets.token_hex(32)
    binary=tmp/"server"
    subprocess.run(["go","build","-o",str(binary),"./cmd/server"],cwd=ROOT,check=True)
    with socket.socket() as sock:
        sock.bind(("127.0.0.1",0)); port=sock.getsockname()[1]
    env={k:v for k,v in os.environ.items() if not k.startswith("TG_")}
    env.update(ADMIN_TOKEN=token,LISTEN_ADDR=f"127.0.0.1:{port}",DATA_DIR=str(tmp/"data"))
    cache=tmp/"data/champions";cache.mkdir(parents=True)
    (cache/"champions.json").write_text(json.dumps({"version":"1.0.0","champions":[{"id":"Garen","name":"盖伦"}]}))
    base=f"http://127.0.0.1:{port}"
    def api(path,data=None):
        req=urllib.request.Request(base+"/api/"+path,data=None if data is None else json.dumps(data).encode(),headers={"Authorization":"Bearer "+token,"Content-Type":"application/json","Idempotency-Key":str(uuid.uuid4())})
        with urllib.request.urlopen(req,timeout=15) as r:return json.load(r)["data"]
    with (tmp/"server.log").open("w") as log:
        proc=subprocess.Popen([str(binary)],env=env,stdout=log,stderr=log)
        try:
            for _ in range(150):
                try: api("state");break
                except OSError: time.sleep(.1)
            else: raise RuntimeError("startup failed")
            names=["清风","北辰","小满","星河","山海","听雨","微光"]
            amounts=["6427.323","3280.500","1000.199","886.700","320.000","88.250","0.001"]
            for i in range(62):
                ident=100000101+i
                api("accounts",{"telegram_id":ident,"name":names[i] if i<7 else "验收玩家"+str(i),"enabled":True})
                if i<7 or i>=12:api("adjustments",{"account_id":"tg:"+str(ident),"delta":amounts[i] if i<7 else "1.000","note":"隔离测试"})
            with sqlite3.connect(tmp/"data/rift-ledger.db") as db:
                db.execute("INSERT INTO telegram_users VALUES(100000107,'tiny_balance','','',1,1)")
            node=os.environ["FILTER_TEST_NODE"]
            script=str(ROOT/"scripts/player_filter_browser.cjs")
            artifacts=str(ROOT/"docs/test-artifacts")
            if node.endswith(".exe"):
                script=subprocess.check_output(["wslpath","-w",script],text=True).strip()
                artifacts=subprocess.check_output(["wslpath","-w",artifacts],text=True).strip()
            result=subprocess.run([node,script],input=json.dumps({"base":base,"token":token,"artifacts":artifacts}),text=True,capture_output=True,timeout=120)
            if result.returncode: raise RuntimeError(result.stderr)
            print(result.stdout)
        finally:proc.terminate();proc.wait(timeout=20)
