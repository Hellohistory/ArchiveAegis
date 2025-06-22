// Package downloader file: internal/downloader/downloader.go
package downloader

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
)

// Downloader =============================================================================
//
//	Downloader 接口 —— 各协议下载器统一规格
//
// =============================================================================
type Downloader interface {
	SupportsScheme(scheme string) bool
	Download(sourceURL *url.URL) (io.ReadCloser, error)
}

// HTTPDownloader =============================================================================
//
//	HTTPDownloader —— 处理 http / https 协议
//
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
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()

		bodyBytes, readErr := io.ReadAll(io.LimitReader(resp.Body, 512))
		if readErr != nil {
			return nil, fmt.Errorf(
				"HTTP请求失败: 状态码 %d，URL: %s，读取响应体失败: %v",
				resp.StatusCode, sourceURL.String(), readErr,
			)
		}
		return nil, fmt.Errorf(
			"HTTP请求失败: 状态码 %d，URL: %s，响应内容: %s",
			resp.StatusCode, sourceURL.String(), string(bodyBytes),
		)
	}
	// 👉 调用方负责 Close
	return resp.Body, nil
}

// FileDownloader =============================================================================
//
//	FileDownloader —— 处理 file:// 协议
//
// =============================================================================
type FileDownloader struct{}

func (d *FileDownloader) SupportsScheme(scheme string) bool {
	return scheme == "file"
}

func (d *FileDownloader) Download(sourceURL *url.URL) (io.ReadCloser, error) {
	path := ResolveLocalFilePath(sourceURL)
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("无法打开本地文件 '%s': %w", path, err)
	}
	return file, nil
}

// ResolveLocalFilePath =============================================================================
//
//	ResolveLocalFilePath —— 【导出】file:// → 本地路径（跨平台）
//	  - Windows:  file:///C:/dir/file.zip  ->  C:\dir\file.zip
//	  - Unix   :  file:///home/a/b.zip     ->  /home/a/b.zip
//
// =============================================================================
func ResolveLocalFilePath(sourceURL *url.URL) string {
	// url.Path 已经做过 %XX 解码
	path := filepath.FromSlash(sourceURL.Path)

	// Windows 处理：去掉「/C:/」开头的多余斜杠
	if len(path) > 0 && path[0] == filepath.Separator {
		// 判断第二个字符是否是盘符冒号
		if len(path) > 2 && path[2] == ':' {
			path = path[1:]
		}
	}
	return path
}
