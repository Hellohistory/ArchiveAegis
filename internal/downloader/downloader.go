// Package downloader file: internal/downloader/downloader.go
package downloader

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
)

// Downloader 是所有下载器都必须实现的接口。
type Downloader interface {
	// SupportsScheme 支持的协议 (e.g., "http", "https", "file")
	SupportsScheme(scheme string) bool
	// Download 执行下载，返回一个可读取文件内容的对象
	Download(sourceURL *url.URL) (io.ReadCloser, error)
}

// =============================================================================
// HTTPDownloader —— 支持 http/https 协议的下载器实现
// =============================================================================

type HTTPDownloader struct {
	Client *http.Client
}

func (d *HTTPDownloader) SupportsScheme(scheme string) bool {
	return scheme == "http" || scheme == "https"
}

func (d *HTTPDownloader) Download(sourceURL *url.URL) (io.ReadCloser, error) {
	resp, err := d.Client.Get(sourceURL.String())
	if err != nil {
		return nil, fmt.Errorf("HTTP请求失败: %w", err)
	}

	// 非 200 响应处理
	if resp.StatusCode != http.StatusOK {
		defer func() {
			if err := resp.Body.Close(); err != nil {
				log.Printf("警告: 关闭非200响应的Body失败: %v", err)
			}
		}()

		bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 512))
		if readErr != nil {
			return nil, fmt.Errorf("HTTP请求失败: 状态码 %d，URL: %s，读取响应体失败: %v",
				resp.StatusCode, sourceURL.String(), readErr)
		}
		return nil, fmt.Errorf("HTTP请求失败: 状态码 %d，URL: %s，响应内容: %s",
			resp.StatusCode, sourceURL.String(), string(bodyBytes))
	}

	// 调用方应自行 Close resp.Body
	return resp.Body, nil
}

// =============================================================================
// FileDownloader —— 支持 file:// 协议的下载器实现（本地文件复制）
// =============================================================================

type FileDownloader struct{}

func (d *FileDownloader) SupportsScheme(scheme string) bool {
	return scheme == "file"
}

func (d *FileDownloader) Download(sourceURL *url.URL) (io.ReadCloser, error) {
	path := resolveLocalFilePath(sourceURL)
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("无法打开本地文件 '%s': %w", path, err)
	}
	return file, nil
}

// resolveLocalFilePath 将 file:// URL 转换为本地操作系统路径（兼容 Windows）
func resolveLocalFilePath(sourceURL *url.URL) string {
	path := filepath.FromSlash(sourceURL.Path)

	// Windows 情况下可能为 /C:/xxx，需要移除前导斜杠
	if len(path) > 0 && path[0] == filepath.Separator {
		if len(path) > 2 && path[2] == ':' {
			path = path[1:]
		}
	}
	return path
}
