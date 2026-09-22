#!/usr/bin/env python3
"""Linux/Docker recovery bundles. Never restores over a live volume or starts a bot.

Bundle contents include credentials: keep the entire directory private.
All external command output is captured; errors deliberately omit secret output.
"""
import argparse
import contextlib
import datetime as dt
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import stat
import subprocess
import sys
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]
NAME = re.compile(r"^bundle-\d{8}T\d{6}Z-[0-9a-f]{32}$")
FILES = {"ledger.db", "environment.env", "container-environment.json", "runtime-settings.json",
         "champions.json", "compose.yaml", "inspection.json"}
REQUIRED = {"ledger.db", "environment.env", "container-environment.json", "compose.yaml", "inspection.json"}


class RecoveryError(Exception):
    pass


def run(args, cwd=ROOT, timeout=180):
    try:
        return subprocess.run(args, cwd=cwd, check=True, stdout=subprocess.PIPE,
                              stderr=subprocess.PIPE, timeout=timeout).stdout
    except (subprocess.CalledProcessError, subprocess.TimeoutExpired, OSError):
        # docker inspect/config/cat output can contain credentials; never echo it.
        raise RecoveryError("外部操作失败或超时（输出已隐藏以保护配置密钥）") from None


def regular(path):
    return stat.S_ISREG(path.lstat().st_mode)


def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def write(path, data):
    with path.open("xb") as stream:
        os.chmod(path, 0o600)
        stream.write(data)
        stream.flush()
        os.fsync(stream.fileno())


def copy_file(source, destination):
    with source.open("rb") as src, destination.open("xb") as dst:
        os.chmod(destination, 0o600)
        shutil.copyfileobj(src, dst, 1024 * 1024)
        dst.flush()
        os.fsync(dst.fileno())


def json_bytes(value):
    return (json.dumps(value, ensure_ascii=False, indent=2) + "\n").encode()


def fsync_dir(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def atomic_json(path, value):
    tmp = path.with_name("." + path.name + "." + uuid.uuid4().hex)
    try:
        write(tmp, json_bytes(value))
        os.replace(tmp, path)
        fsync_dir(path.parent)
    finally:
        tmp.unlink(missing_ok=True)


@contextlib.contextmanager
def locked(store):
    store.mkdir(mode=0o700, parents=True, exist_ok=True)
    if store.is_symlink() or not store.is_dir():
        raise RecoveryError("备份目录必须是实际目录")
    os.chmod(store, 0o700)
    fd = os.open(store / ".lock", os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, "w") as lock:
        try:
            fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise RecoveryError("另一项备份操作正在执行") from None
        yield


class Docker:
    def __init__(self, root):
        self.root = root

    def command(self, *args, timeout=180):
        return run(["docker", *args], self.root, timeout)

    def identity(self):
        cid = self.command("compose", "ps", "-q", "app").decode().strip()
        if not re.fullmatch(r"[0-9a-f]{12,64}", cid):
            raise RecoveryError("需要且只能有一个运行中的 app 容器")
        info = json.loads(self.command("inspect", cid))[0]
        if not info["State"]["Running"]:
            raise RecoveryError("app 容器未运行")
        return {"container": info["Id"], "started_at": info["State"]["StartedAt"],
                "image": info["Image"], "environment": info["Config"]["Env"]}

    def optional(self, cid, filename):
        # Paths are internal constants, never supplied by an archive/manifest.
        raw = self.command("exec", cid, "sh", "-c",
                           'if [ -e "$1" ]; then cat "$1"; else printf MISSING; fi', "sh", filename)
        return None if raw == b"MISSING" else raw

    def capacity(self, cid, store):
        logical = int(self.command("exec", cid, "sqlite3", "-readonly", "/data/rift-ledger.db",
                                  "SELECT page_count*page_size FROM pragma_page_count(),pragma_page_size();"))
        volume_free = int(self.command("exec", cid, "df", "-Pk", "/data").decode().splitlines()[-1].split()[3]) * 1024
        # Source temporary snapshot and host copy can occupy the same disk.
        # Keep 512MiB for normal service operation, even on very small databases.
        required = logical * 3 + 512 * 1024 * 1024
        if min(volume_free, shutil.disk_usage(store).free) < required:
            raise RecoveryError("磁盘余量不足：需容纳临时快照并保留至少512MiB运行空间；未删除旧备份")

    def snapshot(self, cid, target):
        name = "/data/.recovery-" + uuid.uuid4().hex + ".db"
        try:
            self.command("exec", cid, "sh", "-c",
                         'umask 077; sqlite3 -cmd ".timeout 10000" /data/rift-ledger.db "$1"',
                         "sh", ".backup '" + name + "'", timeout=600)
            self.command("cp", cid + ":" + name, str(target), timeout=600)
            os.chmod(target, 0o600)
        finally:
            self.command("exec", cid, "rm", "-f", name)

    def inspect(self, image, database):
        if not re.fullmatch(r"sha256:[0-9a-f]{64}", image):
            raise RecoveryError("恢复包镜像标识无效")
        if "," in str(database):
            raise RecoveryError("Docker备份路径不支持逗号")
        result = json.loads(self.command("run", "--rm", "--network", "none", "--read-only",
            "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
            "--user", f"{os.getuid()}:{os.getgid()}",
            "--mount", f"type=bind,src={database},dst=/backup.db,readonly",
            "--entrypoint", "/usr/local/bin/rift-dbcheck", image, "-db", "/backup.db", timeout=600))
        if result.get("ok") is not True:
            raise RecoveryError("备份账本核验未通过")
        return result


def verify_files(bundle):
    if bundle.is_symlink() or not bundle.is_dir():
        raise RecoveryError("恢复包必须是实际目录")
    manifest_path = bundle / "manifest.json"
    if not regular(manifest_path):
        raise RecoveryError("恢复包缺少清单")
    manifest = json.loads(manifest_path.read_bytes())
    files = manifest.get("files", {})
    if manifest.get("format") != 1 or not REQUIRED <= files.keys() or not files.keys() <= FILES:
        raise RecoveryError("恢复包清单不完整或格式不支持")
    if {p.name for p in bundle.iterdir()} != set(files) | {"manifest.json"}:
        raise RecoveryError("恢复包包含未登记文件")
    for name, expected in files.items():
        path = bundle / name
        if not regular(path) or path.stat().st_size != expected["bytes"] or digest(path) != expected["sha256"]:
            raise RecoveryError("恢复包文件校验失败：" + name)
    return manifest


def verify(bundle, docker):
    manifest = verify_files(bundle)
    report = docker.inspect(manifest["image"], bundle / "ledger.db")
    # Detect any mutation during the independent database check too.
    verify_files(bundle)
    return manifest, report


def prune(store, keep, protected):
    # Only this program's complete, hash-valid bundles are eligible; never touch
    # legacy .db backups, partial bundles, corrupt bundles or unknown directories.
    valid = []
    for path in store.iterdir():
        if NAME.fullmatch(path.name) and path.is_dir() and not path.is_symlink():
            try:
                manifest = verify_files(path)
                valid.append((path == protected, manifest["completed_at"], path.name, path))
            except (RecoveryError, OSError, ValueError, KeyError, TypeError):
                continue
    for _, _, _, path in sorted(valid, reverse=True)[keep:]:
        shutil.rmtree(path)
    fsync_dir(store)


def backup(root, store, keep, docker):
    if keep < 3:
        raise RecoveryError("至少保留3份已验证备份")
    with locked(store):
        previous = {}
        status = store / "status.json"
        if status.exists():
            previous = json.loads(status.read_bytes())
        started = time.time()
        stage = Path(tempfile.mkdtemp(prefix=".partial-", dir=store))
        try:
            initial = docker.identity()
            cid = initial["container"]
            docker.capacity(cid, store)
            environment = (root / ".env").read_bytes()
            runtime = docker.optional(cid, "/data/runtime-settings.json")
            champions = docker.optional(cid, "/data/champions/champions.json")
            docker.snapshot(cid, stage / "ledger.db")
            os.chmod(stage / "ledger.db", 0o600)
            if (docker.identity() != initial or (root / ".env").read_bytes() != environment
                    or docker.optional(cid, "/data/runtime-settings.json") != runtime):
                raise RecoveryError("备份期间配置或容器发生变化，请重新备份")
            write(stage / "environment.env", environment)
            write(stage / "container-environment.json", json_bytes(initial["environment"]))
            write(stage / "compose.yaml", (root / "compose.yaml").read_bytes())
            for name, data in [("runtime-settings.json", runtime), ("champions.json", champions)]:
                if data is not None:
                    json.loads(data)  # fail closed on truncated configuration/cache
                    write(stage / name, data)
            report = docker.inspect(initial["image"], stage / "ledger.db")
            write(stage / "inspection.json", json_bytes(report))
            files = {}
            for path in stage.iterdir():
                with path.open("rb") as stream:
                    os.fsync(stream.fileno())
                files[path.name] = {"bytes": path.stat().st_size, "sha256": digest(path)}
            commit = run(["git", "rev-parse", "HEAD"], root).decode().strip()
            manifest = {"format": 1, "started_at": started, "completed_at": time.time(),
                        "git_commit": commit, "image": initial["image"],
                        "schema_version": report["schema_version"], "files": files}
            write(stage / "manifest.json", json_bytes(manifest))
            fsync_dir(stage)
            verify_files(stage)
            name = "bundle-" + dt.datetime.now(dt.timezone.utc).strftime("%Y%m%dT%H%M%SZ-") + uuid.uuid4().hex
            destination = store / name
            os.rename(stage, destination)
            fsync_dir(store)
            # A failed attempt never prunes previous recovery points.
            prune(store, keep, destination)
            atomic_json(status, {"ok": True, "last_attempt": started, "last_success": time.time(),
                                 "latest": name, "duration_seconds": round(time.time() - started, 3)})
            return destination
        except Exception:
            atomic_json(status, {"ok": False, "last_attempt": started,
                                 "last_success": previous.get("last_success"), "latest": previous.get("latest"),
                                 "error": "备份失败；之前的已完成备份仍保留。检查服务状态、磁盘及权限。"})
            raise
        finally:
            if stage.exists():
                shutil.rmtree(stage)


def prepare(bundle, destination, docker):
    # Refuse an existing target, including an empty directory or a broken symlink.
    if destination.exists() or destination.is_symlink():
        raise RecoveryError("恢复目标必须是不存在的新目录；不会覆盖任何已有数据")
    manifest, report = verify(bundle, docker)
    destination.mkdir(mode=0o700)
    try:
        data = destination / "data"
        data.mkdir(mode=0o700)
        copy_file(bundle / "ledger.db", data / "rift-ledger.db")
        if digest(data / "rift-ledger.db") != manifest["files"]["ledger.db"]["sha256"]:
            raise RecoveryError("恢复复制时备份发生变化")
        # Never copy a live token into a runnable rehearsal environment.
        settings = {"enabled": False, "token": "", "mute_on_close": False}
        write(data / "runtime-settings.json", json_bytes(settings))
        if (bundle / "champions.json").exists():
            (data / "champions").mkdir(mode=0o700)
            write(data / "champions" / "champions.json", (bundle / "champions.json").read_bytes())
        write(destination / "inspection.json", json_bytes(report))
        write(destination / ".env", ("ADMIN_TOKEN=" + secrets.token_hex(32) + "\n").encode())
        # No ports and no network: even an operator changing settings cannot send.
        compose = {"services": {"review": {"image": manifest["image"], "network_mode": "none",
            "user": f"{os.getuid()}:{os.getgid()}", "read_only": True, "cap_drop": ["ALL"],
            "security_opt": ["no-new-privileges:true"], "env_file": ".env",
            "environment": {"DATA_DIR": "/data", "LISTEN_ADDR": "127.0.0.1:8080"},
            "volumes": ["./data:/data"], "tmpfs": ["/tmp:size=16m,mode=1777"], "restart": "no"}}}
        write(destination / "compose.yaml", json_bytes(compose))  # JSON is valid YAML
        write(destination / "RECOVERY.txt", "隔离恢复副本：无网络、无真实Token、无对外端口。\n此目录不能直接用于正式接管。先核实备份后账目、offset及消息送达，再按恢复文档人工接管。\n".encode())
        fsync_dir(data)
        fsync_dir(destination)
    except Exception:
        # Directory was created exclusively by us; never contains existing data.
        shutil.rmtree(destination)
        raise
    return destination


def health(store, max_age):
    status = json.loads((store / "status.json").read_bytes())
    latest = status.get("latest", "")
    if not isinstance(latest, str) or not NAME.fullmatch(latest):
        raise RecoveryError("尚无成功备份")
    manifest = verify_files(store / latest)
    # Fresh completion of a very slow backup must not hide an old recovery point.
    age = time.time() - manifest["started_at"]
    if not status.get("ok") or age < 0 or age > max_age:
        raise RecoveryError("最近备份失败、已过期或服务器时间异常")
    return {"ok": True, "latest": latest, "age_seconds": round(age), "offsite": False}


def main():
    os.umask(0o077)
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=ROOT)
    sub = parser.add_subparsers(dest="command", required=True)
    b = sub.add_parser("backup")
    b.add_argument("--keep", type=int, default=96)
    s = sub.add_parser("status")
    s.add_argument("--max-age", type=int, default=3600)
    v = sub.add_parser("verify")
    v.add_argument("bundle", type=Path)
    p = sub.add_parser("prepare")
    p.add_argument("bundle", type=Path)
    p.add_argument("destination", type=Path)
    args = parser.parse_args()
    root = args.root.resolve()
    store = root / "backups" / "recovery"
    docker = Docker(root)
    try:
        if args.command == "backup":
            print("成套备份完成：" + str(backup(root, store, args.keep, docker)))
        elif args.command == "status":
            if args.max_age <= 0:
                raise RecoveryError("max-age必须大于0")
            print(json.dumps(health(store, args.max_age), ensure_ascii=False))
        elif args.command == "verify":
            _, report = verify(args.bundle.absolute(), docker)
            print(json.dumps(report, ensure_ascii=False, indent=2))
        else:
            print("隔离恢复目录已生成（未启动服务）：" + str(prepare(args.bundle.absolute(), args.destination.absolute(), docker)))
    except (RecoveryError, OSError, ValueError, KeyError, TypeError) as exc:
        print(str(exc) if isinstance(exc, RecoveryError) else "恢复操作失败：文件、配置或清单无效；未输出敏感内容。", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
