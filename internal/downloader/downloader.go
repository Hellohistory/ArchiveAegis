// file: internal/downloader/downloader.go
package downloader

import (
	"fmt"
	"io"
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

// HTTPDownloader =============================================================================
//
//	HTTP/HTTPS 下载器实现
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
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close() // 确保在出错时关闭body
		return nil, fmt.Errorf("HTTP请求失败, 状态码: %d", resp.StatusCode)
	}
	return resp.Body, nil
}

// FileDownloader =============================================================================
//
//	本地文件“下载”器 (实际上是文件复制)
//
// =============================================================================
type FileDownloader struct{}

func (d *FileDownloader) SupportsScheme(scheme string) bool {
	return scheme == "file"
}

func (d *FileDownloader) Download(sourceURL *url.URL) (io.ReadCloser, error) {
	// url.Parse 会将本地路径转换为 URL 结构，其 Path 字段是我们需要的
	// 例如 "file:///C:/Users/..." -> Path: "/C:/Users/..."
	// 在 Windows 上需要去掉这个前导斜杠
	path := filepath.FromSlash(sourceURL.Path)

	// 对于 Windows 路径 (e.g., C:\...), TrimPrefix 会移除开头的 '\'
	if len(path) > 0 && path[0] == filepath.Separator {
		if len(path) > 2 && path[2] == ':' { // 检查是否是 "C:" 这样的驱动器号
			path = path[1:]
		}
	}

	return os.Open(path)
}
