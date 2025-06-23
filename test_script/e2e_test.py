import os
import requests
import time
import sys
import json
import sqlite3
import shutil
import subprocess
import atexit

# ==============================================================================
# --- 配置区 ---
# ==============================================================================
BASE_URL = "http://localhost:10224/api/v1"
GATEWAY_EXE_PATH = os.path.join("../AegisBuild", "ArchiveAegisCore.exe")

ADMIN_USER = "admin"
ADMIN_PASS = "password"
PLUGIN_ID = "io.archiveaegis.sqlite"
PLUGIN_VERSION = "1.0.0"

INSTANCE_DIR = "../instance"
BIZ_DIR = os.path.join(INSTANCE_DIR, "sales_data")
DB_PATH = os.path.join(BIZ_DIR, "2025_sales.db")

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


def terminate_existing_gateway_processes():
    """
    尝试终止所有可能正在运行的网关进程，以确保文件锁被释放。
    """
    print_info("正在尝试终止所有可能存在的 ArchiveAegisCore 进程...")
    if sys.platform == "win32":
        try:
            # /F 强制终止进程，/IM 指定镜像名称
            subprocess.run(["taskkill", "/F", "/IM", os.path.basename(GATEWAY_EXE_PATH)], check=False,
                           capture_output=True)
            print_info(f"已尝试终止 '{os.path.basename(GATEWAY_EXE_PATH)}' 进程。")
        except FileNotFoundError:
            print_info("taskkill 命令未找到，可能不是 Windows 系统或 PATH 配置有问题。")
        except Exception as e:
            print_info(f"终止进程时发生错误: {e}")
    else: # For Linux/macOS
        try:
            subprocess.run(["pkill", "-f", os.path.basename(GATEWAY_EXE_PATH)], check=False, capture_output=True)
            print_info(f"已尝试终止 '{os.path.basename(GATEWAY_EXE_PATH)}' 进程。")
        except FileNotFoundError:
            print_info("pkill 命令未找到，可能不是类 Unix 系统或 PATH 配置有问题。")
        except Exception as e:
            print_info(f"终止进程时发生错误: {e}")
    time.sleep(1) # 给一些时间让系统释放文件句柄


def prepare_test_environment():
    """
    准备测试环境：清理旧数据，确保每次测试都是干净的开始，并创建测试数据库。
    """
    print_step("A", "准备测试环境 (清理旧数据，创建数据库)")
    try:
        # 确保终止可能存在的旧网关进程，释放文件锁
        terminate_existing_gateway_processes()

        # 清理旧的 instance 目录和 auth.db，确保每次测试都是干净的开始
        if os.path.exists(INSTANCE_DIR):
            print_info(f"清理旧的实例目录: {INSTANCE_DIR}")
            shutil.rmtree(INSTANCE_DIR)
        # Auth.db 通常在 instance 目录下，随 instance 目录一起删除即可

        os.makedirs(BIZ_DIR, exist_ok=True)
        print_info(f"已确保测试数据目录存在: {BIZ_DIR}")

        # 如果数据库已存在则跳过，因为上面已经清理了整个 INSTANCE_DIR
        if os.path.exists(DB_PATH):
            print_info(f"数据库已存在，跳过创建: {DB_PATH}")
            return

        conn = sqlite3.connect(DB_PATH)
        cursor = conn.cursor()
        cursor.execute('''
                       CREATE TABLE "sales_records"
                       (
                           "id"           INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
                           "product_name" TEXT,
                           "region"       TEXT,
                           "sale_amount"  REAL,
                           "sale_date"    TEXT
                       );''')
        sales = [
            ('Aegis Pro', '北美', 199.99, '2025-01-15'), ('Aegis Lite', '欧洲', 99.50, '2025-01-20'),
            ('Archive Hub', '亚洲', 499.00, '2025-02-10'), ('Aegis Pro', '欧洲', 205.00, '2025-02-18'),
            ('Data Core', '北美', 1200.00, '2025-03-05')
        ]
        cursor.executemany(
            'INSERT INTO sales_records (product_name, region, sale_amount, sale_date) VALUES (?, ?, ?, ?)', sales)
        conn.commit()
        conn.close()
        print_status("成功创建并填充测试数据库。")
    except Exception as e:
        print_status(f"准备测试环境失败: {e}", success=False)


def start_gateway():
    """启动网关服务作为子进程。"""
    global gateway_process
    print_step("B", "启动网关服务子进程")
    try:
        print_info(f"正在从 '{GATEWAY_EXE_PATH}' 启动网关...")
        # 注意：这里只启动一次网关进程，避免端口冲突
        gateway_process = subprocess.Popen([GATEWAY_EXE_PATH])
        print_status(f"网关进程已启动，PID: {gateway_process.pid}")
    except FileNotFoundError:
        print_status(f"未找到网关可执行文件: '{GATEWAY_EXE_PATH}'。请先编译 Go 项目。", success=False)
    except Exception as e:
        print_status(f"启动网关失败: {e}", success=False)


def wait_for_gateway():
    """等待网关服务就绪，通过健康检查接口轮询。"""
    print_info("等待网关服务就绪...")
    for i in range(10):  # 最多等待10秒，每次1秒
        try:
            requests.get(f"{BASE_URL}/system/status", timeout=1)
            print_status("网关已就绪，可以开始测试。")
            return
        except requests.exceptions.ConnectionError:
            time.sleep(1)
    print_status("等待网关超时，测试中止。", success=False)


def cleanup():
    """确保无论如何都关闭网关进程。"""
    if gateway_process:
        print_info(f"\n测试结束，正在尝试关闭网关进程 (PID: {gateway_process.pid})...")
        # 使用 terminate() 发送 SIGTERM，给进程一个机会优雅退出
        gateway_process.terminate()
        try:
            # 等待进程完全终止，设置超时
            gateway_process.wait(timeout=5)
            print_status("网关进程已成功关闭。")
        except subprocess.TimeoutExpired:
            print_info(f"警告: 网关进程 (PID: {gateway_process.pid}) 未能在5秒内优雅关闭，将强制终止。")
            gateway_process.kill() # 强制终止
            gateway_process.wait()
            print_status("网关进程已强制关闭。")
    else:
        print_info("\n没有正在运行的网关进程需要关闭。")


# 注册一个退出函数，确保脚本无论如何退出（成功、失败、Ctrl+C），都会尝试关闭网关
atexit.register(cleanup)


def perform_setup_and_get_token(session):
    """执行首次安装，创建管理员并获取JWT。"""
    print_info("系统处于首次安装模式，开始自动化设置...")
    resp = session.get(f"{BASE_URL}/system/setup")
    resp.raise_for_status() # 如果状态码不是2xx，会抛出异常
    setup_token = resp.json().get("token")
    if not setup_token:
        print_status("未能从 /setup 接口获取到安装令牌", success=False)
    print_info(f"成功获取到安装令牌: {setup_token}")
    setup_payload = {"token": setup_token, "user": ADMIN_USER, "pass": ADMIN_PASS}
    resp = session.post(f"{BASE_URL}/system/setup", json=setup_payload)
    if resp.status_code != 200:
        print_status(f"创建管理员失败，状态码: {resp.status_code}, 响应: {resp.text}", success=False)
    token = resp.json().get("token")
    print_status("成功创建管理员并获取到初始 JWT。")
    return token


def perform_login_and_get_token(session):
    """执行标准登录流程，获取JWT。"""
    print_info("系统已安装，执行标准登录流程...")
    login_payload = {"user": ADMIN_USER, "pass": ADMIN_PASS}
    resp = session.post(f"{BASE_URL}/auth/login", json=login_payload)
    if resp.status_code != 200:
        print_status(f"登录失败，状态码: {resp.status_code}, 响应: {resp.text}", success=False)
    token = resp.json().get("token")
    print_status("成功登录。")
    return token


def run_api_tests(session):
    """执行所有API测试步骤。"""
    instance_id = None # 用于在 finally 块中停止和删除实例
    try:
        # 步骤 1: 智能认证 (首次安装或登录)
        print_step(1, "智能认证")
        status_resp = session.get(f"{BASE_URL}/system/status")
        status_resp.raise_for_status()
        system_status = status_resp.json().get("status")
        if system_status == "needs_setup":
            token = perform_setup_and_get_token(session)
        elif system_status == "ready_for_login":
            token = perform_login_and_get_token(session)
        else:
            print_status(f"未知的系统状态: {system_status}", success=False)
        session.headers.update({"Authorization": f"Bearer {token}"})
        print_info("认证完成，JWT 已自动应用于后续所有请求。")

        # 步骤 2: 查看可用插件列表
        print_step(2, "查看可用插件列表")
        resp = session.get(f"{BASE_URL}/admin/plugins/available")
        resp.raise_for_status()
        available_plugins = resp.json()['data']
        assert any(p['id'] == PLUGIN_ID for p in available_plugins)
        print_status(f"成功获取到可用插件列表，并找到目标插件 {PLUGIN_ID}")

        # 步骤 3: 安装 SQLite 插件
        print_step(3, "安装 SQLite 插件")
        install_payload = {"plugin_id": PLUGIN_ID, "version": PLUGIN_VERSION}
        resp = session.post(f"{BASE_URL}/admin/plugins/install", json=install_payload)
        resp.raise_for_status()
        print_status(f"插件安装任务已成功提交: {resp.json()['message']}")

        # 步骤 4: 创建插件实例 (销售数据服务)
        print_step(4, "创建插件实例 (销售数据服务)")
        instance_payload = {"display_name": "我的销售数据服务", "plugin_id": PLUGIN_ID, "version": PLUGIN_VERSION,
                            "biz_name": "sales_data"}
        resp = session.post(f"{BASE_URL}/admin/plugins/instances", json=instance_payload)
        resp.raise_for_status()
        instance_id = resp.json().get('instance_id')
        assert instance_id
        print_status(f"成功创建插件实例，Instance ID: {instance_id}")

        # 步骤 5: 启动并验证插件实例
        print_step(5, "启动并验证插件实例")
        resp = session.post(f"{BASE_URL}/admin/plugins/instances/{instance_id}/start")
        resp.raise_for_status()
        print_status("插件实例启动任务已成功提交...")
        print_info("后台正在启动插件进程，等待其就绪...")
        for i in range(10): # 最多等待20秒
            time.sleep(2)
            print(f"  轮询第 {i + 1}/10 次...")
            resp = session.get(f"{BASE_URL}/admin/plugins/instances")
            resp.raise_for_status()
            instances = resp.json()['data']
            target_instance = next((inst for inst in instances if inst['instance_id'] == instance_id), None)
            if target_instance and target_instance['status'] == 'RUNNING':
                print_status(f"实例 {instance_id} 状态正确: RUNNING")
                break
        else:
            print_status(f"超时！实例 {instance_id} 未能在20秒内进入 RUNNING 状态", success=False)

        # 步骤 6: 配置业务组和表Schema (核心修复步骤)
        print_step(6, "配置业务组和表Schema")

        # 6.1 配置业务组总体设置
        print_info("配置业务组 'sales_data' 总体设置...")
        biz_settings_payload = {
            "is_publicly_searchable": True,
            "default_query_table": "sales_records"
        }
        resp = session.put(f"{BASE_URL}/admin/biz-config/sales_data/settings", json=biz_settings_payload)
        resp.raise_for_status()
        print_status("业务组 'sales_data' 总体设置成功。")

        # 6.2 配置业务组可搜索表
        print_info("配置业务组 'sales_data' 可搜索表...")
        searchable_tables_payload = {
            "searchable_tables": ["sales_records"]
        }
        resp = session.put(f"{BASE_URL}/admin/biz-config/sales_data/tables", json=searchable_tables_payload)
        resp.raise_for_status()
        print_status("业务组 'sales_data' 可搜索表配置成功。")

        # 6.3 配置表的字段设置 (必须精确匹配数据库中的列名和数据类型)
        print_info("配置表 'sales_records' 字段设置...")
        field_settings_payload = [
            {"field_name": "id", "is_searchable": True, "is_returnable": True, "data_type": "integer"},
            {"field_name": "product_name", "is_searchable": True, "is_returnable": True, "data_type": "text"},
            {"field_name": "region", "is_searchable": True, "is_returnable": True, "data_type": "text"},
            {"field_name": "sale_amount", "is_searchable": True, "is_returnable": True, "data_type": "real"},
            {"field_name": "sale_date", "is_searchable": True, "is_returnable": True, "data_type": "text"},
        ]
        resp = session.put(f"{BASE_URL}/admin/biz-config/sales_data/tables/sales_records/fields",
                           json=field_settings_payload)
        resp.raise_for_status()
        print_status("表 'sales_records' 字段设置成功。")

        # 6.4 配置表的写权限 (可选，但推荐在测试中明确设置，尤其对于mutate操作)
        print_info("配置表 'sales_records' 写权限...")
        permissions_payload = {
            "allow_create": True,
            "allow_update": True,
            "allow_delete": True
        }
        resp = session.put(f"{BASE_URL}/admin/biz-config/sales_data/tables/sales_records/permissions",
                           json=permissions_payload)
        resp.raise_for_status()
        print_status("表 'sales_records' 写权限配置成功。")

        # 步骤 7: 验证数据查询 API (最终检验)
        print_step(7, "验证数据查询 API (最终检验)")
        query_payload = {"biz_name": "sales_data", "query": {"table": "sales_records"}}
        resp = session.post(f"{BASE_URL}/data/query", json=query_payload)
        resp.raise_for_status()

        response_data_wrapper = resp.json().get('data', {})
        query_data = []
        if isinstance(response_data_wrapper, list):
            query_data = response_data_wrapper
        elif isinstance(response_data_wrapper, dict):
            for key, value in response_data_wrapper.items():
                if isinstance(value, list):
                    query_data = value
                    print_info(f"在响应的 'data' 对象中找到列表，键名为 '{key}'")
                    break

        assert len(query_data) == 5, f"期望获取5条数据，实际获取到 {len(query_data)} 条"
        print_status(f"数据查询成功！成功从动态启动的插件中获取到 {len(query_data)} 条数据。")
        print_info("查询结果预览: " + json.dumps(query_data[0], ensure_ascii=False))

    except requests.exceptions.RequestException as e:
        print_status(f"发生网络或HTTP错误: {e}", success=False)
    except Exception as e:
        print_status(f"发生未知错误: {e}", success=False)

    finally:
        # 确保清理发生在 `instance_id` 已设置的情况下，即使在 earlier failure
        if instance_id:
            print_step(8, "清理插件实例") # 将清理步骤编号改为 8
            try:
                # 尝试停止实例
                resp = session.post(f"{BASE_URL}/admin/plugins/instances/{instance_id}/stop")
                if resp.ok:
                    print_status("插件实例停止任务已成功提交。")
                else:
                    print_info(f"注意: 停止实例时出现错误: {resp.text}")
                time.sleep(2) # 给予一些时间让插件进程真正停止

                # 尝试删除实例配置
                resp = session.delete(f"{BASE_URL}/admin/plugins/instances/{instance_id}")
                if resp.ok:
                    print_status("插件实例配置已成功删除。")
                else:
                    print_info(f"注意: 删除实例时出现错误: {resp.text}")
            except Exception as e:
                print_info(f"清理插件实例过程中发生错误: {e}")


# ==============================================================================
# --- 主执行入口 ---
# ==============================================================================
if __name__ == "__main__":

    prepare_test_environment() # 更新后的环境准备函数，包含清理
    start_gateway()
    wait_for_gateway()
    run_api_tests(requests.Session())

    print("\n" + "🏆 " * 3 + " 恭喜！全流程端到端自动化测试成功！ " + "🏆 " * 3)
