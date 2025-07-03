#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import atexit
import os
import subprocess
import sys
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from pathlib import Path

import requests

# --- 配置 ---
BASE_URL = os.getenv("AEGIS_BASE_URL", "http://localhost:10224/api/v1")
ADMIN_USER = "admin"
ADMIN_PASS = "password"
FIREWALL_RULE_NAME = "Aegis_CI_Test_Rule"

gateway_path_str = os.getenv("AEGIS_BIN")
if gateway_path_str:
    GATEWAY_EXE_PATH = Path(gateway_path_str)
else:
    _gateway_base_path = Path("./AegisBuild/ArchiveAegisCore")
    if sys.platform == "win32":
        GATEWAY_EXE_PATH = _gateway_base_path.with_suffix(".exe").resolve()
    else:
        GATEWAY_EXE_PATH = _gateway_base_path.resolve()

GATEWAY_LOG_FILE = Path("gateway-output.log").resolve()
gateway_process = None

# --- CI 专用日志函数 ---
def log(level, message):
    """统一的日志输出函数，FAIL级别会终止程序。"""
    print(f"[{level.upper():<5}] {message}", flush=True)
    if level.upper() == "FAIL":
        sys.exit(1)


# --- Windows 防火墙自动化 ---
def configure_windows_firewall():
    log("INFO", "Windows: Adding firewall rule...")
    command = [
        "netsh", "advfirewall", "firewall", "add", "rule", f'name="{FIREWALL_RULE_NAME}"',
        "dir=in", "action=allow", f'program="{GATEWAY_EXE_PATH}"', "enable=yes"
    ]
    try:
        subprocess.run(command, check=True, capture_output=True)
    except subprocess.CalledProcessError as e:
        stderr_decoded = e.stderr.decode('gbk', errors='ignore')
        if "已存在" in stderr_decoded or "exists" in stderr_decoded.lower():
             log("INFO", "Windows: Firewall rule already exists.")
        else:
            log("FAIL", f"Windows: Failed to add firewall rule. Error: {stderr_decoded}")


def cleanup_windows_firewall_rule():
    log("INFO", f"Windows: Cleaning up firewall rule '{FIREWALL_RULE_NAME}'...")
    command = ["netsh", "advfirewall", "firewall", "delete", "rule", f'name="{FIREWALL_RULE_NAME}"']
    subprocess.run(command, check=False, capture_output=True)


# --- 网关进程管理 ---
def start_gateway():
    global gateway_process
    log("STEP", "Starting gateway process...")
    if not GATEWAY_EXE_PATH.exists():
        log("FAIL", f"Gateway executable not found: {GATEWAY_EXE_PATH}")

    log_file_handle = GATEWAY_LOG_FILE.open("w", encoding="utf-8")
    gateway_process = subprocess.Popen([str(GATEWAY_EXE_PATH)], stdout=log_file_handle, stderr=subprocess.STDOUT)
    log("INFO", f"Gateway process started with PID: {gateway_process.pid}")


def wait_for_gateway():
    log("INFO", "Waiting for gateway to become ready...")
    for _ in range(20):  # CI 环境可能稍慢，增加等待次数
        try:
            r = requests.get(f"{BASE_URL}/system/status", timeout=2)
            if r.status_code == 200:
                log("PASS", "Gateway is ready.")
                return
        except requests.exceptions.RequestException:
            time.sleep(1)
    log("FAIL", "Timeout waiting for gateway. Check gateway-output.log for errors.")


def cleanup_gateway_process():
    global gateway_process
    if gateway_process:
        log("INFO", f"Terminating gateway process (PID: {gateway_process.pid})...")
        gateway_process.terminate()
        try:
            gateway_process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            gateway_process.kill()
            gateway_process.wait()
        log("INFO", "Gateway process terminated.")

        if GATEWAY_LOG_FILE.exists() and GATEWAY_LOG_FILE.stat().st_size > 0:
            print("\n--- Gateway Log Output ---")
            print(GATEWAY_LOG_FILE.read_text(encoding="utf-8").strip())
            print("--- End Gateway Log ---\n")
        gateway_process = None


def prepare_environment():
    log("STEP", "Preparing test environment...")
    gateway_basename = GATEWAY_EXE_PATH.name
    log("INFO", f"Terminating any existing '{gateway_basename}' processes...")
    if sys.platform == "win32":
        subprocess.run(["taskkill", "/F", "/IM", gateway_basename], check=False, capture_output=True)
    else:
        subprocess.run(["pkill", "-f", gateway_basename], check=False, capture_output=True)

    # *** 已修正 *** 使用 './' 确保目录在项目文件夹内
    instance_dir = Path("./instance")
    instance_dir.mkdir(exist_ok=True)
    auth_db_path = instance_dir / "auth.db"
    if auth_db_path.exists():
        log("INFO", f"Removing old auth database: {auth_db_path}")
        auth_db_path.unlink()
    if GATEWAY_LOG_FILE.exists():
        GATEWAY_LOG_FILE.unlink()


# --- 测试核心逻辑 ---
def initial_setup_and_get_token(session):
    log("INFO", "Performing initial system setup...")
    resp = session.get(f"{BASE_URL}/system/setup")
    if resp.status_code != 200: log("FAIL", f"GET /system/setup failed with status {resp.status_code}")

    setup_token = resp.json().get("token")
    if not setup_token: log("FAIL", "Could not retrieve setup token.")

    payload = {"token": setup_token, "user": ADMIN_USER, "pass": ADMIN_PASS}
    resp = session.post(f"{BASE_URL}/system/setup", json=payload)
    if resp.status_code != 200: log("FAIL", f"POST /system/setup failed with status {resp.status_code}")

    token = resp.json().get("token")
    if not token: log("FAIL", "Could not retrieve JWT after setup.")
    log("PASS", "Initial setup complete and JWT obtained.")
    return token


def run_burst_test(name, url, burst_count, expected_ok_max, session=None):
    log("STEP", f"Testing rate limit for: {name}")
    log("INFO", f"Sending {burst_count} concurrent requests to {url}...")

    success_count = throttled_count = 0
    client = session or requests

    with ThreadPoolExecutor(max_workers=burst_count) as executor:
        futures = [executor.submit(client.get, url, timeout=5) for _ in range(burst_count)]
        for f in as_completed(futures):
            try:
                code = f.result().status_code
                if code == 200:
                    success_count += 1
                elif code == 429:
                    throttled_count += 1
                else:
                    log("FAIL", f"Received unexpected status code: {code}")
            except requests.exceptions.RequestException as e:
                log("FAIL", f"Request failed with exception: {e}")

    log("INFO", f"Results - Success: {success_count}, Throttled: {throttled_count}")
    if not throttled_count > 0:
        log("FAIL", "Rate limit failed: No requests were throttled.")
    if success_count > expected_ok_max:
        log("FAIL", f"Rate limit failed: Success count ({success_count}) exceeded expected max ({expected_ok_max}).")
    log("PASS", f"Rate limit test for '{name}' passed.")


# --- 主执行流程 ---
def main():
    if sys.platform == "win32":
        configure_windows_firewall()

    prepare_environment()
    start_gateway()
    wait_for_gateway()

    run_burst_test(
        name="Anonymous IP Limit",
        url=f"{BASE_URL}/system/status",
        burst_count=35,
        expected_ok_max=25
    )

    log("INFO", "Waiting for rate limiter recovery...")
    time.sleep(3)

    session = requests.Session()
    jwt = initial_setup_and_get_token(session)
    session.headers.update({"Authorization": f"Bearer {jwt}"})

    run_burst_test(
        name="Authenticated Business API Limit",
        url=f"{BASE_URL}/meta/biz",
        burst_count=45,
        expected_ok_max=35,
        session=session
    )

    log("STEP", "All tests completed successfully.")


if __name__ == "__main__":
    atexit.register(cleanup_gateway_process)
    if sys.platform == "win32":
        atexit.register(cleanup_windows_firewall_rule)

    main()
