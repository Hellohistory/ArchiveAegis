// file: internal/downloader/downloader_test.go

package downloader

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// 支持的协议判断
func TestSupportsScheme(t *testing.T) {
	httpd := &HTTPDownloader{}
	filed := &FileDownloader{}

	cases := []struct {
		dl      Downloader
		scheme  string
		wantHit bool
	}{
		{httpd, "http", true},
		{httpd, "https", true},
		{httpd, "file", false},
		{filed, "file", true},
		{filed, "http", false},
	}

	for _, c := range cases {
		if got := c.dl.SupportsScheme(c.scheme); got != c.wantHit {
			t.Errorf("%T.SupportsScheme(%q) = %v, 期望 %v",
				c.dl, c.scheme, got, c.wantHit)
		}
	}
}

// HTTPDownloader.Download
func TestHTTPDownloader_Download_Success(t *testing.T) {
	// 建立一个返回 200 OK 的本地服务器
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello world"))
	}))
	defer server.Close()

	parsed, _ := url.Parse(server.URL)

	dl := &HTTPDownloader{Client: server.Client()}
	rc, err := dl.Download(parsed)
	if err != nil {
		t.Fatalf("Download 应成功，却返回错误: %v", err)
	}
	defer rc.Close()

	body, _ := io.ReadAll(rc)
	if !bytes.Equal(body, []byte("hello world")) {
		t.Errorf("响应内容不匹配: 得到 %q", string(body))
	}
}

func TestHTTPDownloader_Download_NonOK(t *testing.T) {
	// 返回 404 并携带简短响应体
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
	}))
	defer server.Close()

	parsed, _ := url.Parse(server.URL)

	dl := &HTTPDownloader{Client: server.Client()}
	_, err := dl.Download(parsed)
	if err == nil {
		t.Fatal("预期错误，但得到 nil")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("404")) {
		t.Errorf("错误信息应包含状态码 404, 得到: %v", err)
	}
}

// FileDownloader.Download
func TestFileDownloader_Download_Success(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "a.txt")

	// 创建临时文件
	if err := os.WriteFile(filePath, []byte("local data"), 0644); err != nil {
		t.Fatalf("无法写入临时文件: %v", err)
	}

	u := &url.URL{
		Scheme: "file",
		Path:   filepath.ToSlash(filePath), // 转为 file:// 兼容的路径形式
	}

	dl := &FileDownloader{}
	rc, err := dl.Download(u)
	if err != nil {
		t.Fatalf("FileDownloader.Download 失败: %v", err)
	}
	defer rc.Close()

	body, _ := io.ReadAll(rc)
	if string(body) != "local data" {
		t.Errorf("文件内容不符: 得到 %q", string(body))
	}
}

func TestFileDownloader_Download_NotFound(t *testing.T) {
	u, _ := url.Parse("file:///path/does/not/exist.txt")
	dl := &FileDownloader{}
	_, err := dl.Download(u)
	if err == nil {
		t.Fatal("预期文件不存在错误，但返回 nil")
	}
}

// ResolveLocalFilePath
func TestResolveLocalFilePath(t *testing.T) {
	tests := []struct {
		rawURL  string
		wantWin string // Windows 期望
		wantNix string // Unix 期望
	}{
		{
			rawURL:  "file:///C:/dir/file.zip",
			wantWin: `C:\dir\file.zip`,
			wantNix: `C:/dir/file.zip`,
		},
		{
			rawURL:  "file:///home/user/data.bin",
			wantWin: `\home\user\data.bin`,
			wantNix: `/home/user/data.bin`,
		},
	}

	for _, tt := range tests {
		u, err := url.Parse(tt.rawURL)
		if err != nil {
			t.Fatalf("解析 URL 失败: %v", err)
		}
		got := ResolveLocalFilePath(u)

		var want string
		if runtime.GOOS == "windows" {
			want = filepath.Clean(tt.wantWin)
		} else {
			want = filepath.Clean(tt.wantNix)
		}

		if got != want {
			t.Errorf("%s 解析错误: for %q -> got %q, want %q", runtime.GOOS, tt.rawURL, got, want)
		}
	}
}

func TestDownloader_InterfaceUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("abc"))
	}))
	defer server.Close()

	httpURL, _ := url.Parse(server.URL)
	fileURL, _ := url.Parse("file:///non-existent.txt")

	dls := []Downloader{
		&HTTPDownloader{Client: server.Client()},
		&FileDownloader{},
	}

	for _, d := range dls {
		var target *url.URL
		if d.SupportsScheme("http") {
			target = httpURL
		} else {
			target = fileURL
		}
		_, _ = d.Download(target) // 这里只验证接口可调用，不关心结果
	}
	fmt.Println("接口多态调用示例完成")
}
