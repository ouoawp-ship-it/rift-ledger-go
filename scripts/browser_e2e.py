#!/usr/bin/env python3
"""Isolated browser acceptance test. Needs Linux, Go, gcc, SQLite headers, Python + Playwright.
Never connects a Telegram token or opens the production data directory.
Run: python3 scripts/browser_e2e.py (CHROMIUM_PATH can override /usr/bin/chromium).
"""
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[1]

def main():
    from playwright.sync_api import sync_playwright, expect
    report = {"scenario": "isolated Linux browser + HTTP acceptance", "real_telegram": False, "checks": []}
    artifacts = ROOT / "docs" / "test-artifacts"
    artifacts.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="rift-go-browser-") as temp:
        temp = Path(temp)
        binary = temp / "server"
        subprocess.run(["go", "build", "-trimpath", "-o", str(binary), "./cmd/server"], cwd=ROOT, check=True)
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        base = f"http://127.0.0.1:{port}"
        secret = secrets.token_hex(32)
        environment = {k: v for k, v in os.environ.items() if not k.startswith("TG_")}
        environment.update(ADMIN_TOKEN=secret, LISTEN_ADDR=f"127.0.0.1:{port}", DATA_DIR=str(temp / "data"))
        def api(path, body=None):
            headers = {"Authorization": "Bearer " + secret}
            if body is not None:
                headers.update({"Content-Type": "application/json", "Idempotency-Key": str(uuid.uuid4())})
            request = urllib.request.Request(base + "/api/" + path, headers=headers, data=None if body is None else json.dumps(body).encode())
            with urllib.request.urlopen(request, timeout=10) as response:
                value = json.load(response)
            assert value["ok"], value
            return value["data"]
        with (temp / "server.log").open("w") as logfile:
            process = subprocess.Popen([str(binary)], env=environment, stdout=logfile, stderr=subprocess.STDOUT)
            try:
                for attempt in range(150):
                    if process.poll() is not None:
                        raise RuntimeError((temp / "server.log").read_text())
                    try:
                        with urllib.request.urlopen(base + "/healthz", timeout=0.2):
                            break
                    except OSError:
                        time.sleep(0.1)
                else:
                    raise RuntimeError("Test server did not become ready")
                with sync_playwright() as pw:
                    executable = os.environ.get("CHROMIUM_PATH") or shutil.which("chromium")
                    browser = pw.chromium.launch(executable_path=executable, headless=True, args=["--no-sandbox"])
                    page = browser.new_page(viewport={"width": 1366, "height": 900}, locale="zh-CN")
                    errors = []
                    page.on("pageerror", lambda err: errors.append(str(err)))
                    page.on("dialog", lambda dialog: dialog.accept())
                    page.goto(base, wait_until="networkidle")
                    page.locator("#token").fill(secret)
                    page.locator('#login-form button').click()
                    expect(page.locator("#workspace")).to_be_visible()
                    expect(page.locator("#notice")).to_contain_text("已连接")
                    assert page.evaluate("localStorage.length + sessionStorage.length") == 0
                    assert page.locator("#token").input_value() == ""
                    report["checks"].append("Login; token cleared from input; no browser storage")
                    page.locator('[data-tab="calculator"]').click()
                    page.locator('#calc-form button').click()
                    expect(page.locator("#calc-result")).to_contain_text("牛9")
                    expect(page.locator("#calc-result")).to_contain_text("牛1")
                    expect(page.locator("#calc-result")).to_contain_text("净盈利倍数：3")
                    page.screenshot(path=str(artifacts / "calculator.png"), full_page=True)
                    report["checks"].append("Calculator: 12745 beats 12710, own payout 3")
                    page.locator('[data-tab="rules"]').click()
                    for selector, value in [("#fee-timing", "settlement"), ("#void-fee", "refund"), ("#fee-recipient", "fees"), ("#zero-triple", "true")]:
                        page.locator(selector).select_option(value)
                    page.locator('#rules-confirmed').check()
                    page.locator('#rules-form button[type="submit"]').click()
                    expect(page.locator("#notice")).to_contain_text("规则设置已保存")
                    report["checks"].append("First-run explicit rule confirmation through browser")
                    page.locator('[data-tab="players"]').click()
                    page.locator('#player-tg').fill("123456789")
                    page.locator('#player-name').fill("隔离测试玩家")
                    page.locator('#player-form button').click()
                    expect(page.locator("#notice")).to_contain_text("玩家资格已保存")
                    for account, delta in [("house", "10000"), ("tg:123456789", "1000")]:
                        page.locator('#adjust-id').fill(account)
                        page.locator('#adjust-delta').fill(delta)
                        page.locator('#adjust-note').fill("浏览器隔离验收，无实际资金")
                        page.locator('#adjust-form button[type="submit"]').click()
                        expect(page.locator("#notice")).to_contain_text("调分成功，账户 " + account)
                    report["checks"].append("Manual whitelist and two audited balance adjustments through browser")
                    page.locator('[data-tab="round"]').click()
                    page.locator('#number').fill("2026-09-20-0001")
                    page.locator('#create-round').click()
                    expect(page.locator("#notice")).to_contain_text("首期草稿已创建")
                    heroes = [("Aatrox", "暗裔剑魔"), ("Ahri", "九尾妖狐"), ("Ashe", "寒冰射手"), ("Garen", "德玛西亚之力"), ("Yasuo", "疾风剑豪")]
                    for i, (hid, name) in enumerate(heroes, 1):
                        page.locator(f'#hero-id-{i}').fill(hid)
                        page.locator(f'#hero-name-{i}').fill(name)
                    assert page.locator('input[name="banker"]:checked').count() == 0
                    page.locator('#banker-3').check()
                    page.locator('#save-heroes').click()
                    expect(page.locator("#notice")).to_contain_text("英雄与庄家已保存")
                    page.locator('#open-round').click()
                    expect(page.locator("#round-state")).to_contain_text("受理中")
                    expect(page.locator('#hero-id-1')).to_be_disabled()
                    current = api("state")["active_round"]
                    api("bets", {"account_id": "tg:123456789", "round_id": current["id"], "position": 1, "stake": 100})
                    accounts = {a["id"]: a for a in api("accounts")}
                    assert accounts["tg:123456789"]["locked"] == 101
                    assert accounts["house"]["locked"] == 400
                    report["checks"].append("Manual banker 3, locked heroes; admin API test bet; player 101 / house 400 frozen")
                    page.locator('#close-round').click()
                    expect(page.locator("#round-state")).to_contain_text("已封盘")
                    page.locator('#duration').fill("1200")
                    for i, damage in enumerate(["12745", "99999", "12710", "12737", "12345"], 1):
                        page.locator(f'#damage-{i}').fill(damage)
                    page.locator('#preview').click()
                    expect(page.locator("#notice")).to_contain_text("预览已生成")
                    expect(page.locator('#settle')).to_be_enabled()
                    page.locator('#duration').fill("1201")
                    expect(page.locator('#settle')).to_be_disabled()
                    page.locator('#preview').click()
                    expect(page.locator('#settle')).to_be_enabled()
                    page.screenshot(path=str(artifacts / "settlement-preview.png"), full_page=True)
                    report["checks"].append("Preview rendering; changed input invalidates prior confirmation")
                    page.locator('#settle').click()
                    expect(page.locator("#notice")).to_contain_text("已结算入账")
                    expect(page.locator("#round-state")).to_contain_text("2026-09-20-0002期 · 待配置")
                    assert page.locator('input[name="banker"]:checked').count() == 0
                    assert page.locator('#hero-id-1').input_value() == ""
                    accounts = {a["id"]: a for a in api("accounts")}
                    assert accounts["tg:123456789"]["balance"] == 1299
                    assert accounts["house"]["balance"] == 9700
                    assert accounts["fees"]["balance"] == 1
                    assert all(a["locked"] == 0 for a in accounts.values())
                    result = api("reconcile")
                    assert result["balanced"] is True and result["issues"] == []
                    report["checks"].append("Final player1299 / house9700 / fees1; zero frozen; auto-next draft with no banker; reconciliation passes")
                    page.locator('[data-tab="history"]').click()
                    expect(page.locator('#history-table')).to_contain_text('2026-09-20-0001')
                    page.locator(f'button[data-round="{current["id"]}"]').click()
                    expect(page.locator('#history-detail')).to_contain_text('牛9')
                    page.locator('[data-tab="checks"]').click()
                    page.locator('#reconcile').click()
                    expect(page.locator('#reconcile-result')).to_contain_text('检查通过')
                    page.screenshot(path=str(artifacts / "reconciliation.png"), full_page=True)
                    page.set_viewport_size({"width": 1366, "height": 768})
                    page.locator('[data-tab="round"]').click()
                    assert page.evaluate('document.documentElement.scrollWidth <= window.innerWidth')
                    page.screenshot(path=str(artifacts / "next-round.png"), full_page=True)
                    report["checks"].append("History and reconciliation pages; 1366x768 layout without page horizontal overflow")
                    assert errors == [], errors
                    report["browser"] = browser.version
                    report["javascript_errors"] = errors
                    report["final_balances"] = {a: accounts[a]["balance"] for a in ["tg:123456789", "house", "fees"]}
                    browser.close()
            finally:
                process.terminate()
                try:
                    process.wait(timeout=25)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait(timeout=5)
            report["server_exit"] = process.returncode
            assert process.returncode == 0, (temp / "server.log").read_text()
    report["passed"] = True
    output = json.dumps(report, ensure_ascii=False, indent=2)
    (artifacts / "browser-report.json").write_text(output + "\n", encoding="utf-8")
    print(output)

if __name__ == "__main__":
    main()
