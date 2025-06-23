import atexit
import os
import subprocess
import sys
import time
from concurrent.futures import ThreadPoolExecutor, as_completed

import requests

# ==============================================================================
# --- 配置区 ---
# ==============================================================================
BASE_URL = "http://localhost:10224/api/v1"
GATEWAY_EXE_PATH = os.path.join("../AegisBuild", "ArchiveAegisCore.exe")

ADMIN_USER = "admin"
ADMIN_PASS = "password"

# 全局变量，用于确保总能关闭子进程
gateway_process = None

# ==============================================================================
# --- 辅助函数 ---
# ==============================================================================
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
        if gateway_process:
            print_info("测试失败，正在尝试关闭网关进程...")
            gateway_process.terminate()
        sys.exit(1)

def print_info(message):
    """打印一般信息。"""
    print(f"   ℹ️  {message}")

# ==============================================================================
# --- 网关启动与清理 ---
# ==============================================================================
def terminate_existing_gateway_processes():
    """尝试终止所有可能正在运行的网关进程。"""
    print_info("正在尝试终止所有可能存在的 ArchiveAegisCore 进程...")
    try:
        if sys.platform == "win32":
            subprocess.run(["taskkill", "/F", "/IM", os.path.basename(GATEWAY_EXE_PATH)], check=False, capture_output=True)
        else:
            subprocess.run(["pkill", "-f", os.path.basename(GATEWAY_EXE_PATH)], check=False, capture_output=True)
        print_info(f"已尝试终止 '{os.path.basename(GATEWAY_EXE_PATH)}' 进程。")
    except Exception as e:
        print_info(f"终止进程时发生错误: {e}")
    time.sleep(1)

def prepare_test_environment():
    """准备测试环境：仅清理认证数据库。"""
    print_step("A", "准备测试环境 (清理旧数据)")
    terminate_existing_gateway_processes()
    instance_dir = "../instance"
    os.makedirs(instance_dir, exist_ok=True)
    auth_db_path = os.path.join(instance_dir, "auth.db")
    if os.path.exists(auth_db_path):
        print_info(f"清理旧的认证数据库: {auth_db_path}")
        os.remove(auth_db_path)

def start_gateway():
    """启动网关服务作为子进程。"""
    global gateway_process
    print_step("B", "启动网关服务子进程")
    try:
        print_info(f"正在从 '{GATEWAY_EXE_PATH}' 启动网关...")
        gateway_process = subprocess.Popen([GATEWAY_EXE_PATH])
        print_status(f"网关进程已启动，PID: {gateway_process.pid}")
    except FileNotFoundError:
        print_status(f"未找到网关可执行文件: '{GATEWAY_EXE_PATH}'。", success=False)
    except Exception as e:
        print_status(f"启动网关失败: {e}", success=False)

def wait_for_gateway():
    """等待网关服务就绪。"""
    print_info("等待网关服务就绪...")
    for _ in range(15):
        try:
            r = requests.get(f"{BASE_URL}/system/status", timeout=1)
            if r.status_code == 200:
                print_status("网关已就绪，可以开始测试。")
                return
        except requests.exceptions.RequestException:
            time.sleep(1)
    print_status("等待网关超时，测试中止。", success=False)

def cleanup():
    """确保无论如何都关闭网关进程。"""
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

atexit.register(cleanup)

# ==============================================================================
# --- 认证流程 ---
# ==============================================================================
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

# ==============================================================================
# --- 限速测试核心逻辑 ---
# ==============================================================================

def send_request(url, session=None):
    """发送单个请求并返回状态码的辅助函数"""
    try:
        s = session or requests
        resp = s.get(url, timeout=5)
        return resp.status_code
    except requests.exceptions.RequestException:
        return 500 # 返回一个错误码

def run_ratelimit_tests():
    """执行所有限速相关的测试。"""
    # 步骤 1: 测试全局 IP 限速 (无需认证)
    # 根据日志，IP默认限制为 瞬时峰值 20
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

    # --- BUG 修复点 ---
    # 在进行下一步操作前，等待几秒钟，让IP限速器的令牌桶恢复。
    # 否则，下一步的认证请求会因为IP仍处于被限速状态而失败。
    wait_for_recovery = 3
    print_info(f"等待 {wait_for_recovery} 秒以使IP限速器恢复...")
    time.sleep(wait_for_recovery)


    # 步骤 2: 用户认证
    print_step(2, "用户认证")
    session = requests.Session()
    token = initial_setup_and_get_token(session)
    session.headers.update({"Authorization": f"Bearer {token}"})
    print_info("认证完成，JWT 已自动应用于后续所有请求。")

    # 步骤 3: 测试业务接口速率限制 (认证后)
    # 根据日志，业务接口限制为 瞬时峰值 30
    print_step(3, "测试业务接口速率限制 (已认证)")
    burst_limit_authenticated = 30
    requests_to_send = burst_limit_authenticated + 15
    success_count = 0
    throttled_count = 0

    print_info(f"将向 /api/v1/meta/biz 并发发送 {requests_to_send} 个请求...")
    with ThreadPoolExecutor(max_workers=requests_to_send) as executor:
        futures = [executor.submit(send_request, f"{BASE_URL}/meta/biz", session) for _ in range(requests_to_send)]
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

    # 步骤 4: 验证限速恢复
    print_step(4, "验证速率限制恢复")
    wait_time = 2
    print_info(f"等待 {wait_time} 秒让限速器恢复...")
    time.sleep(wait_time)

    resp = session.get(f"{BASE_URL}/meta/biz")
    if resp.status_code == 200:
        print_status("速率限制已恢复，请求成功。")
    else:
        print_status(f"速率限制恢复失败，请求返回 {resp.status_code}", success=False)

# ==============================================================================
# --- 主执行入口 ---
# ==============================================================================
if __name__ == "__main__":
    prepare_test_environment()
    start_gateway()
    wait_for_gateway()

    run_ratelimit_tests()

    print("\n" + "🏆 " * 3 + " 恭喜！速率限制自动化测试成功！ " + "🏆 " * 3)
