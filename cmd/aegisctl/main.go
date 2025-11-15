// Package main 提供插件管理命令行工具入口
// 文件位置: cmd/aegisctl/main.go
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"text/tabwriter"
)

const (
	defaultGateway     = "http://127.0.0.1:10224"
	pluginPathFragment = "/api/v1/admin/plugins"
)

type pluginListResponse struct {
	Data []pluginInstance `json:"data"`
}

type pluginInstance struct {
	InstanceID         string     `json:"instance_id"`
	DisplayName        string     `json:"display_name"`
	PluginID           string     `json:"plugin_id"`
	Version            string     `json:"version"`
	BizName            string     `json:"biz_name"`
	Status             string     `json:"status"`
	RuntimePID         *int       `json:"runtime_pid"`
	HealthStatus       string     `json:"health_status"`
	ProtocolVersion    string     `json:"protocol_version"`
	CircuitBreakerOpen bool       `json:"circuit_breaker_open"`
	LastHeartbeatRaw   *time.Time `json:"last_heartbeat"`
}

func main() {
	os.Exit(run())
}

func run() int {
	global := flag.NewFlagSet("aegisctl", flag.ExitOnError)
	gateway := global.String("gateway", defaultGateway, "ArchiveAegis 网关基础地址")
	token := global.String("token", "", "用于认证的 Bearer Token")
	timeout := global.Duration("timeout", 10*time.Second, "HTTP 请求超时时间")
	if err := global.Parse(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "解析参数失败: %v\n", err)
		return 2
	}
	if global.NArg() < 1 {
		printUsage()
		return 1
	}

	switch global.Arg(0) {
	case "plugin":
		return handlePluginSubcommand(*gateway, *token, *timeout, global.Args()[1:])
	default:
		printUsage()
		return 1
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "用法: aegisctl [全局参数] <命令> [命令参数]\n")
	fmt.Fprintf(os.Stderr, "可用命令:\n")
	fmt.Fprintf(os.Stderr, "  plugin list                       列出所有插件实例\n")
	fmt.Fprintf(os.Stderr, "  plugin start <instance-id>        启动指定插件实例\n")
	fmt.Fprintf(os.Stderr, "  plugin stop <instance-id>         停止指定插件实例\n")
	fmt.Fprintf(os.Stderr, "  plugin reload <instance-id>       重载指定插件实例\n")
}

func handlePluginSubcommand(baseURL, token string, timeout time.Duration, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "缺少 plugin 子命令")
		printUsage()
		return 1
	}

	pluginBase, err := joinURL(baseURL, pluginPathFragment)
	if err != nil {
		fmt.Fprintf(os.Stderr, "无效的网关地址: %v\n", err)
		return 1
	}

	client := &http.Client{Timeout: timeout}

	switch args[0] {
	case "list":
		if err := listPlugins(client, pluginBase, token); err != nil {
			fmt.Fprintf(os.Stderr, "列出插件实例失败: %v\n", err)
			return 1
		}
		return 0
	case "start", "stop", "reload":
		if len(args) < 2 {
			fmt.Fprintf(os.Stderr, "命令 '%s' 需要指定实例 ID\n", args[0])
			return 1
		}
		if err := execInstanceAction(client, pluginBase, token, args[0], args[1]); err != nil {
			fmt.Fprintf(os.Stderr, "执行插件操作失败: %v\n", err)
			return 1
		}
		fmt.Printf("✅ 已完成插件实例 '%s' 的 %s 操作。\n", args[1], args[0])
		return 0
	default:
		fmt.Fprintf(os.Stderr, "未知的 plugin 子命令: %s\n", args[0])
		printUsage()
		return 1
	}
}

func listPlugins(client *http.Client, baseURL, token string) error {
	req, err := http.NewRequest(http.MethodGet, baseURL+"/instances", nil)
	if err != nil {
		return err
	}
	addAuthHeader(req, token)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload pluginListResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return fmt.Errorf("解析响应失败: %w", err)
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "INSTANCE ID\tDISPLAY\tPLUGIN\tVERSION\tBIZ\tSTATUS\tPID\tHEALTH\tPROTOCOL\tCIRCUIT")
	for _, inst := range payload.Data {
		pid := "-"
		if inst.RuntimePID != nil {
			pid = fmt.Sprintf("%d", *inst.RuntimePID)
		}
		circuit := "关闭"
		if inst.CircuitBreakerOpen {
			circuit = "开启"
		}
		fmt.Fprintf(
			tw,
			"%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			inst.InstanceID,
			inst.DisplayName,
			inst.PluginID,
			inst.Version,
			inst.BizName,
			inst.Status,
			pid,
			inst.HealthStatus,
			inst.ProtocolVersion,
			circuit,
		)
	}
	return tw.Flush()
}

func execInstanceAction(client *http.Client, baseURL, token, action, instanceID string) error {
	endpoint := fmt.Sprintf("%s/instances/%s/%s", baseURL, instanceID, action)
	req, err := http.NewRequest(http.MethodPost, endpoint, nil)
	if err != nil {
		return err
	}
	addAuthHeader(req, token)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func addAuthHeader(req *http.Request, token string) {
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Content-Type", "application/json")
}

func joinURL(base, path string) (string, error) {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = strings.TrimRight(u.Path, "/") + path
	return u.String(), nil
}
