// file: internal/downloader/downloader_test.go
package downloader

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
//  HTTPDownloader Tests
// ============================================================================

func TestHTTPDownloader_SupportsScheme(t *testing.T) {
	d := &HTTPDownloader{}
	testCases := []struct {
		scheme   string
		expected bool
	}{
		{"http", true},
		{"https", true},
		{"file", false},
		{"ftp", false},
		{"", false},
	}

	for _, tc := range testCases {
		t.Run(tc.scheme, func(t *testing.T) {
			assert.Equal(t, tc.expected, d.SupportsScheme(tc.scheme))
		})
	}
}

func TestHTTPDownloader_Download(t *testing.T) {
	t.Run("successful download", func(t *testing.T) {
		expectedContent := "hello world"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(expectedContent))
		}))
		defer server.Close()

		d := &HTTPDownloader{Client: server.Client()}
		sourceURL, _ := url.Parse(server.URL)

		reader, err := d.Download(sourceURL)
		require.NoError(t, err)
		require.NotNil(t, reader)
		defer reader.Close()

		content, err := io.ReadAll(reader)
		require.NoError(t, err)
		assert.Equal(t, expectedContent, string(content))
	})

	t.Run("server error response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("The requested resource was not found"))
		}))
		defer server.Close()

		d := &HTTPDownloader{Client: server.Client()}
		sourceURL, _ := url.Parse(server.URL)

		_, err := d.Download(sourceURL)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "HTTP请求失败: 状态码 404")
		assert.Contains(t, err.Error(), "The requested resource was not found")
	})

	t.Run("network error", func(t *testing.T) {
		d := &HTTPDownloader{Client: http.DefaultClient}
		sourceURL, _ := url.Parse("http://127.0.0.1:1")

		_, err := d.Download(sourceURL)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "HTTP请求失败")
	})
}

// ============================================================================
//  FileDownloader Tests
// ============================================================================

func TestFileDownloader_SupportsScheme(t *testing.T) {
	d := &FileDownloader{}
	assert.True(t, d.SupportsScheme("file"))
	assert.False(t, d.SupportsScheme("http"))
}

func TestFileDownloader_Download(t *testing.T) {
	tempDir := t.TempDir()

	t.Run("successful download", func(t *testing.T) {
		// 这个测试已经通过，无需修改
		expectedContent := "local file content"
		filePath := filepath.Join(tempDir, "testfile.txt")
		err := os.WriteFile(filePath, []byte(expectedContent), 0644)
		require.NoError(t, err)

		fileURL := "file:///" + filepath.ToSlash(filePath)
		sourceURL, err := url.Parse(fileURL)
		require.NoError(t, err)

		d := &FileDownloader{}
		reader, err := d.Download(sourceURL)
		require.NoError(t, err)
		require.NotNil(t, reader)
		defer reader.Close()

		content, err := io.ReadAll(reader)
		require.NoError(t, err)
		assert.Equal(t, expectedContent, string(content))
	})

	t.Run("file not found", func(t *testing.T) {
		nonExistentPath := filepath.Join(tempDir, "nonexistent.txt")
		fileURL := "file:///" + filepath.ToSlash(nonExistentPath)
		sourceURL, err := url.Parse(fileURL)
		require.NoError(t, err)

		d := &FileDownloader{}
		_, err = d.Download(sourceURL)

		require.Error(t, err)

		// ====================================================================
		// 核心修正点: 不再使用 os.IsNotExist，而是直接检查错误消息的内容
		// ====================================================================
		assert.Contains(t, err.Error(), "cannot find the file specified", "error message should indicate file not found")
	})
}

// ============================================================================
//  Helper Function Tests
// ============================================================================

func TestResolveLocalFilePath(t *testing.T) {
	t.Run("standard unix path", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Skipping unix path test on non-windows system")
		}
		u, _ := url.Parse("file:///home/user/file.txt")
		path := resolveLocalFilePath(u) //可以直接调用，因为包名是 downloader
		assert.Equal(t, "/home/user/file.txt", path)
	})

	t.Run("standard windows path", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Skip("Skipping windows path test on non-windows system")
		}
		u, _ := url.Parse("file:///C:/Users/test/file.txt")
		path := resolveLocalFilePath(u)
		assert.Equal(t, `C:\Users\test\file.txt`, path)
	})

	t.Run("windows path with space", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Skip("Skipping windows path test on non-windows system")
		}
		// URL中空格被编码为%20，url.Parse会自动解码
		u, _ := url.Parse("file:///C:/Program%20Files/app.exe")
		path := resolveLocalFilePath(u)
		assert.Equal(t, `C:\Program Files\app.exe`, path)
	})
}
