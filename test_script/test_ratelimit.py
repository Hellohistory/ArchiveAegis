#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import atexit
import subprocess
import sys
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from pathlib import Path

import requests

BASE_URL = "http://localhost:10224/api/v1"
ADMIN_USER = "admin"
ADMIN_PASS = "password"

# 使用 pathlib 动态构建路径，并根据操作系统自动添加 .exe
_gateway_base_path = Path("../AegisBuild/ArchiveAegisCore")
if sys.platform == "win32":
    GATEWAY_EXE_PATH = _gateway_base_path.with_suffix(".exe").resolve()
else:
    GATEWAY_EXE_PATH = _gateway_base_path.resolve()

GATEWAY_LOG_FILE = Path("gateway-output.log").resolve()
FIREWALL_RULE_NAME = "Aegis Test Automation Rule"

# 全局变量，用于确保总能关闭子进程
gateway_process = None


def print_step(step_num, title):
    """打印测试步骤的标题。"""
    print("\n" + "=" * 80)
    print(f"▶️  步骤 {step_num}: {title}")
    print("=" * 80)


def print_status(message, success=True):
    """打印测试状态信息，如果失败则退出程序。"""
    prefix = "✅ PASS:" if success else "❌ FAIL:"
    print(f"{prefix} {message}")
    if not success:
        sys.exit(1)  # atexit 会自动调用 cleanup


def print_info(message):
    """打印一般信息。"""
    print(f"   ℹ️  {message}")


def configure_windows_firewall():
    """在 Windows 上，自动添加防火墙规则以允许网关程序通信。"""
    print_info("Windows 环境检测到，正在配置防火墙规则...")
    command = [
        "netsh", "advfirewall", "firewall", "add", "rule",
        f'name="{FIREWALL_RULE_NAME}"',
        "dir=in",
        "action=allow",
        f'program="{GATEWAY_EXE_PATH}"',
        "enable=yes",
        "profile=any"
    ]
    try:
        # 运行命令并隐藏输出
        subprocess.run(command, check=True, capture_output=True, text=True)
        print_status(f"成功添加防火墙规则: '{FIREWALL_RULE_NAME}'")
    except subprocess.CalledProcessError as e:
        print_info(f"添加防火墙规则失败 (可能已存在或需要管理员权限): {e.stderr}")
    except FileNotFoundError:
        print_status("未找到 'netsh' 命令，无法配置防火墙。", success=False)


def cleanup_windows_firewall_rule():
    """在 Windows 上，清理之前添加的防火墙规则。"""
    print_info(f"正在清理 Windows 防火墙规则: '{FIREWALL_RULE_NAME}'...")
    command = [
        "netsh", "advfirewall", "firewall", "delete", "rule",
        f'name="{FIREWALL_RULE_NAME}"'
    ]
    try:
        subprocess.run(command, check=True, capture_output=True, text=True)
        print_status("防火墙规则已成功清理。")
    except (subprocess.CalledProcessError, FileNotFoundError):
        print_info("未能清理防火墙规则 (可能它不存在或 'netsh' 不可用)。")


def terminate_existing_gateway_processes():
    """尝试终止所有可能正在运行的网关进程。"""
    gateway_basename = GATEWAY_EXE_PATH.name
    print_info(f"正在尝试终止所有可能存在的 '{gateway_basename}' 进程...")
    try:
        # --- 平台逻辑分离 ---
        if sys.platform == "win32":
            subprocess.run(["taskkill", "/F", "/IM", gateway_basename], check=False, capture_output=True)
        else:  # Linux, macOS
            subprocess.run(["pkill", "-f", gateway_basename], check=False, capture_output=True)
        print_info(f"已尝试终止历史进程。")
    except Exception as e:
        print_info(f"终止进程时发生错误: {e}")
    time.sleep(1)


def prepare_test_environment():
    """准备测试环境：清理旧数据和日志。"""
    print_step("A", "准备测试环境 (清理旧数据)")
    terminate_existing_gateway_processes()
    instance_dir = Path("../instance")
    instance_dir.mkdir(exist_ok=True)
    auth_db_path = instance_dir / "auth.db"
    if auth_db_path.exists():
        print_info(f"清理旧的认证数据库: {auth_db_path}")
        auth_db_path.unlink()
    # 清理旧的网关日志
    if GATEWAY_LOG_FILE.exists():
        GATEWAY_LOG_FILE.unlink()


def start_gateway():
    """
    启动网关服务作为子进程，并将输出重定向到日志文件 (改进 #2)。
    """
    global gateway_process
    print_step("B", "启动网关服务子进程")
    try:
        print_info(f"正在从 '{GATEWAY_EXE_PATH}' 启动网关...")
        print_info(f"网关日志将被写入: {GATEWAY_LOG_FILE}")

        # 将 stdout 和 stderr 都重定向到同一个日志文件
        log_file_handle = GATEWAY_LOG_FILE.open("w", encoding="utf-8")
        gateway_process = subprocess.Popen(
            [str(GATEWAY_EXE_PATH)],
            stdout=log_file_handle,
            stderr=subprocess.STDOUT
        )
        print_status(f"网关进程已启动，PID: {gateway_process.pid}")
    except FileNotFoundError:
        print_status(f"未找到网关可执行文件: '{GATEWAY_EXE_PATH}'。", success=False)
    except Exception as e:
        print_status(f"启动网关失败: {e}", success=False)


def wait_for_gateway():
    """等待网关服务就绪。"""
    print_info("等待网关服务就绪...")
    for i in range(15):
        try:
            r = requests.get(f"{BASE_URL}/system/status", timeout=2)  # 增加超时到2秒
            if r.status_code == 200:
                print_status("网关已就绪，可以开始测试。")
                return
        except requests.exceptions.RequestException:
            print(f"   ... 等待中 ({i + 1}/15)")
            time.sleep(1)
    print_status("等待网关超时，测试中止。", success=False)


def cleanup():
    """确保无论如何都关闭网关进程，并打印日志。"""
    global gateway_process
    if gateway_process:
        print_info(f"\n测试结束，正在尝试关闭网关进程 (PID: {gateway_process.pid})...")
        gateway_process.terminate()
        try:
            gateway_process.wait(timeout=5)
            print_status("网关进程已成功关闭。")
        except subprocess.TimeoutExpired:
            gateway_process.kill()
            gateway_process.wait()
            print_status("网关进程已强制关闭。")

        if GATEWAY_LOG_FILE.exists():
            print_info("=" * 20 + " 网关完整日志 " + "=" * 20)
            try:
                print(GATEWAY_LOG_FILE.read_text(encoding="utf-8"))
            except Exception as e:
                print_info(f"读取网关日志失败: {e}")
            print_info("=" * 50)

        gateway_process = None

atexit.register(cleanup)
# 仅在 Windows 上，注册清理防火墙规则的函数
if sys.platform == "win32":
    atexit.register(cleanup_windows_firewall_rule)

def initial_setup_and_get_token(session):
    """执行首次安装，创建管理员并获取JWT。"""
    print_info("系统处于首次安装模式，开始自动化设置...")
    resp = session.get(f"{BASE_URL}/system/setup")
    resp.raise_for_status()
    setup_token = resp.json().get("token")
    if not setup_token:
        print_status("未能从 /setup 接口获取到安装令牌", success=False)

    setup_payload = {"token": setup_token, "user": ADMIN_USER, "pass": ADMIN_PASS}
    resp = session.post(f"{BASE_URL}/system/setup", json=setup_payload)
    resp.raise_for_status()
    token = resp.json().get("token")
    print_status("成功创建管理员并获取到初始 JWT。")
    return token


def send_request(url, session=None):
    """发送单个请求并返回状态码的辅助函数"""
    try:
        s = session or requests
        resp = s.get(url, timeout=5)
        return resp.status_code
    except requests.exceptions.RequestException:
        return 500


def run_ratelimit_tests():
    """执行所有限速相关的测试。"""
    print_step(1, "测试全局 IP 速率限制 (未认证)")
    burst_limit_unauthenticated = 20
    requests_to_send = burst_limit_unauthenticated + 15
    print_info(f"将向 /api/v1/system/status 并发发送 {requests_to_send} 个请求...")

    success_count = 0
    throttled_count = 0
    with ThreadPoolExecutor(max_workers=requests_to_send) as executor:
        futures = [executor.submit(send_request, f"{BASE_URL}/system/status") for _ in range(requests_to_send)]
        for future in as_completed(futures):
            status_code = future.result()
            if status_code == 200:
                success_count += 1
            elif status_code == 429:
                throttled_count += 1
            else:
                print_status(f"收到非预期的状态码: {status_code}", success=False)

    print_info(f"测试完成。成功请求: {success_count}，被限速请求: {throttled_count}")
    assert success_count <= burst_limit_unauthenticated + 5
    assert throttled_count > 0
    print_status("全局 IP 速率限制测试通过：成功阻止了超出限制的请求。")

    wait_for_recovery = 3
    print_info(f"等待 {wait_for_recovery} 秒以使IP限速器恢复...")
    time.sleep(wait_for_recovery)

    print_step(2, "用户认证")
    session = requests.Session()
    token = initial_setup_and_get_token(session)
    session.headers.update({"Authorization": f"Bearer {token}"})
    print_info("认证完成，JWT 已自动应用于后续所有请求。")

    print_step(3, "测试业务接口速率限制 (已认证)")
    burst_limit_authenticated = 30
    requests_to_send_authed = burst_limit_authenticated + 15
    success_count = 0
    throttled_count = 0

    print_info(f"将向 /api/v1/meta/biz 并发发送 {requests_to_send_authed} 个请求...")
    with ThreadPoolExecutor(max_workers=requests_to_send_authed) as executor:
        futures = [executor.submit(send_request, f"{BASE_URL}/meta/biz", session) for _ in
                   range(requests_to_send_authed)]
        for future in as_completed(futures):
            status_code = future.result()
            if status_code == 200:
                success_count += 1
            elif status_code == 429:
                throttled_count += 1
            else:
                print_status(f"收到非预期的状态码: {status_code}", success=False)

    print_info(f"测试完成。成功请求: {success_count}，被限速请求: {throttled_count}")
    assert success_count <= burst_limit_authenticated + 5
    assert throttled_count > 0
    print_status("业务接口速率限制测试通过：成功阻止了超出限制的请求。")

    print_step(4, "验证速率限制恢复")
    wait_time = 2
    print_info(f"等待 {wait_time} 秒让限速器恢复...")
    time.sleep(wait_time)

    resp = session.get(f"{BASE_URL}/meta/biz", headers=session.headers)
    if resp.status_code == 200:
        print_status("速率限制已恢复，请求成功。")
    else:
        print_status(f"速率限制恢复失败，请求返回 {resp.status_code}", success=False)

if __name__ == "__main__":
    if sys.platform == "win32":
        configure_windows_firewall()

    prepare_test_environment()
    start_gateway()
    wait_for_gateway()

    run_ratelimit_tests()

    print("\n" + "🏆 " * 3 + " 恭喜！速率限制自动化测试成功！ " + "🏆 " * 3)

