#!/usr/bin/env python3
# -*- coding: utf-8 -*-

from __future__ import annotations

import os
import signal
import subprocess
import sys
import time
from contextlib import contextmanager
from pathlib import Path
from typing import Generator, Tuple
from concurrent.futures import ThreadPoolExecutor, as_completed

import requests

BASE_URL: str = os.getenv("AEGIS_BASE_URL", "http://localhost:10224/api/v1")

_default_bin = Path(os.getenv("AEGIS_BIN", "../AegisBuild/ArchiveAegisCore"))
if sys.platform == "win32":
    _default_bin = _default_bin.with_suffix(".exe")
GATEWAY_BIN: Path = _default_bin.resolve(strict=False)

ADMIN_USER = "admin"
ADMIN_PASS = "password"

INSTANCE_DIR = Path("../instance")

def log(step: str, msg: str) -> None:
    """统一格式化输出"""
    print(f"{step:>8} │ {msg}", flush=True)


def _run(cmd: list[str]) -> None:
    """执行本地命令并忽略返回值（用作清理）"""
    subprocess.run(cmd, check=False, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)


@contextmanager
def launch_gateway(bin_path: Path) -> Generator[subprocess.Popen, None, None]:
    """启动网关并在退出时确保关闭"""
    if not bin_path.exists():
        raise FileNotFoundError(f"未找到网关可执行文件: {bin_path}")

    # 尝试杀掉残留进程
    log("PREP", "终止历史网关进程…")
    if sys.platform == "win32":
        _run(["taskkill", "/F", "/IM", bin_path.name])
    else:
        _run(["pkill", "-f", bin_path.name])

    log("PREP", f"启动网关 ⇢ {bin_path}")
    proc = subprocess.Popen([str(bin_path)], stdout=subprocess.PIPE, stderr=subprocess.STDOUT)

    try:
        _wait_until_ready()
        yield proc
    finally:
        log("CLEAN", f"关闭网关进程 (PID={proc.pid})")
        if proc.poll() is None:
            sig = signal.SIGINT if sys.platform == "win32" else signal.SIGTERM
            proc.send_signal(sig)
            try:
                proc.wait(5)
            except subprocess.TimeoutExpired:
                proc.kill()
        # 输出最后几行日志辅助调试
        if proc.stdout:
            tail = "".join(proc.stdout.readlines()[-20:]).strip()
            if tail:
                log("LOG", f"核心日志尾部 ↓\n{tail}")


def _wait_until_ready(timeout: int = 15) -> None:
    """轮询 /system/status 直到 200 或超时抛异常"""
    start = time.monotonic()
    while time.monotonic() - start < timeout:
        try:
            if requests.get(f"{BASE_URL}/system/status", timeout=1).status_code == 200:
                log("READY", "网关已就绪")
                return
        except requests.exceptions.RequestException:
            pass
        time.sleep(1)
    raise RuntimeError("网关启动超时")

def _burst_get(url: str, burst: int, session: requests.Session | None = None) -> Tuple[int, int]:
    ok = throttled = 0
    with ThreadPoolExecutor(max_workers=burst) as pool:
        futures = [pool.submit(lambda: (session or requests).get(url, timeout=5)) for _ in range(burst)]
        for f in as_completed(futures):
            try:
                code = f.result().status_code
            except requests.exceptions.RequestException:
                code = 500
            if code == 200:
                ok += 1
            elif code == 429:
                throttled += 1
            else:
                raise AssertionError(f"收到非预期状态码 {code}")
    return ok, throttled


def _initial_setup(session: requests.Session) -> str:
    r = session.get(f"{BASE_URL}/system/setup")
    r.raise_for_status()
    setup_token = r.json().get("token")
    if not setup_token:
        raise AssertionError("未获取到安装令牌")

    payload = {"token": setup_token, "user": ADMIN_USER, "pass": ADMIN_PASS}
    resp = session.post(f"{BASE_URL}/system/setup", json=payload)
    resp.raise_for_status()
    jwt = resp.json().get("token")
    if not jwt:
        raise AssertionError("初始化未返回 JWT")
    return jwt

def main() -> None:
    log("STEP A", "清理旧认证数据库")
    INSTANCE_DIR.mkdir(exist_ok=True)
    auth_db = INSTANCE_DIR / "auth.db"
    if auth_db.exists():
        auth_db.unlink()

    with launch_gateway(GATEWAY_BIN):
        log("STEP 1", "匿名速率限制测试")
        ok, throttled = _burst_get(f"{BASE_URL}/system/status", burst=35)
        assert throttled > 0 and ok <= 25

        time.sleep(3)

        sess = requests.Session()
        jwt = _initial_setup(sess)
        sess.headers.update({"Authorization": f"Bearer {jwt}"})
        log("AUTH", "已获取并附加 JWT")

        log("STEP 2", "认证后速率限制测试")
        ok, throttled = _burst_get(f"{BASE_URL}/meta/biz", burst=45, session=sess)
        assert throttled > 0 and ok <= 35

        time.sleep(2)
        assert sess.get(f"{BASE_URL}/meta/biz", timeout=5).status_code == 200
        log("PASS", "速率限制全部验证通过 🎉")

    log("END", "测试脚本执行完毕")


if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        log("ERROR", str(exc))
        sys.exit(1)
