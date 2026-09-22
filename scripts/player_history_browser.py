#!/usr/bin/env python3
"""Isolated player-history browser acceptance. Linux Go + Node Playwright required.
--node may point to a Windows node.exe under WSL; --browser selects local Chromium/Edge.
"""
import argparse
import json
import os
from pathlib import Path
import secrets
import socket
import sqlite3
import subprocess
import tempfile
import time
import urllib.request
import uuid

ROOT=Path(__file__).resolve().parents[1]


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--node',default='node')
    parser.add_argument('--browser',default='')
    args=parser.parse_args()
    with tempfile.TemporaryDirectory(prefix='rift-player-history-') as tmp:
        tmp=Path(tmp)
        binary=tmp/'server'
        subprocess.run(['go','build','-o',str(binary),'./cmd/server'],cwd=ROOT,check=True)
        with socket.socket() as sock:
            sock.bind(('127.0.0.1',0)); port=sock.getsockname()[1]
        base=f'http://127.0.0.1:{port}'
        token=secrets.token_hex(32)
        env={k:v for k,v in os.environ.items() if not k.startswith('TG_')}
        env.update(ADMIN_TOKEN=token,DATA_DIR=str(tmp/'data'),LISTEN_ADDR=f'127.0.0.1:{port}')
        heroes=[{'id':name,'name':name} for name in ['Garen','Ashe','Lux','Jinx','Annie']]
        cache=tmp/'data/champions'
        cache.mkdir(parents=True)
        (cache/'champions.json').write_text(json.dumps({'version':'1.0.0','champions':heroes}))
        def api(path,data=None):
            headers={'Authorization':'Bearer '+token,'Content-Type':'application/json','Idempotency-Key':str(uuid.uuid4())}
            request=urllib.request.Request(base+'/api/'+path,headers=headers,data=None if data is None else json.dumps(data).encode())
            with urllib.request.urlopen(request,timeout=15) as response:
                result=json.load(response);assert result['ok'];return result['data']
        with (tmp/'server.log').open('w') as log:
            process=subprocess.Popen([str(binary)],env=env,stdout=log,stderr=log)
            try:
                for _ in range(150):
                    if process.poll() is not None: raise RuntimeError('fixture server exited')
                    try: state=api('state');break
                    except OSError:time.sleep(.1)
                else:raise RuntimeError('fixture startup timeout')
                rules=state['rules']|{'confirmed':True,'min_stake':.001,'payout':[1.2]*11}
                api('rules',{'expected_version':state['rules_version'],'rules':rules})
                api('accounts',{'telegram_id':111,'name':'历史验收玩家','enabled':True})
                api('accounts',{'telegram_id':222,'name':'另一位玩家','enabled':True})
                for i in range(28):
                    api('adjustments',{'account_id':'tg:111','delta':'2000.199' if i==0 else '0.001','note':'测试上分 '+str(i)})
                r=api('rounds',{'number':'2026-09-22-0001','banker':1,'heroes':heroes})
                prefix='rounds/'+r['id']
                api(prefix+'/open',{})
                for position in [2,3,4]:api('bets',{'account_id':'tg:111','round_id':r['id'],'position':position,'stake':'100.001'})
                api(prefix+'/close',{})
                settlement={'damages':['12745','12737','12710','123','12710']}
                settlement['preview_token']=api(prefix+'/preview',settlement)['token']
                api(prefix+'/settle',settlement)
                with sqlite3.connect(tmp/'data/rift-ledger.db') as db:
                    db.execute("INSERT INTO telegram_users VALUES(111,'player_one','','',1,1)")
                    db.execute("INSERT INTO balance_requests(update_id,account_id,kind,amount,balance_at_request,state,created_at,processed_at,note) VALUES(1,'tg:111','DEBIT',1234,1000199,'REJECTED',?,?, '测试拒绝')",(int(time.time()),int(time.time())))
                config={'base':base,'token':token,'browser':args.browser,'artifacts':str(ROOT/'docs/test-artifacts')}
                script=str(ROOT/'scripts/player_history_browser.cjs')
                if args.node.endswith('.exe'):
                    script=subprocess.check_output(['wslpath','-w',script],text=True).strip()
                    config['artifacts']=subprocess.check_output(['wslpath','-w',config['artifacts']],text=True).strip()
                result=subprocess.run([args.node,script],input=json.dumps(config),text=True,capture_output=True,timeout=120)
                if result.returncode: raise RuntimeError(result.stderr)
                print(result.stdout,end='')
            finally:
                process.terminate();process.wait(timeout=20)


if __name__=='__main__':main()
