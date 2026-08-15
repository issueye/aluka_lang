package gui

import (
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// AssetProvider 定义前端资产提供者（支持内嵌内存 FS 或本地开发目录）。
type AssetProvider interface {
	Open(name string) (io.ReadCloser, string, error) // 返回内容、MIME类型、错误
}

// LocalDirectoryAssetProvider 从本地目录加载资产（开发模式）。
type LocalDirectoryAssetProvider struct {
	BaseDir string
}

func (p *LocalDirectoryAssetProvider) Open(name string) (io.ReadCloser, string, error) {
	cleanPath := filepath.Clean(strings.TrimPrefix(name, "/"))
	if cleanPath == "." || cleanPath == "" {
		cleanPath = "index.html"
	}
	fullPath := filepath.Join(p.BaseDir, cleanPath)

	f, err := os.Open(fullPath)
	if err != nil {
		// 若请求的是目录或未找到，尝试回退 index.html (SPA 路由模式)
		if f2, err2 := os.Open(filepath.Join(p.BaseDir, "index.html")); err2 == nil {
			return f2, "text/html; charset=utf-8", nil
		}
		return nil, "", err
	}

	ext := filepath.Ext(cleanPath)
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = detectMimeType(cleanPath)
	}
	return f, contentType, nil
}

// MemoryAssetProvider 从内存 Map 加载资产（单文件打包发布模式）。
type MemoryAssetProvider struct {
	mu     sync.RWMutex
	assets map[string][]byte
}

// NewMemoryAssetProvider 创建内存资产提供者。
func NewMemoryAssetProvider() *MemoryAssetProvider {
	return &MemoryAssetProvider{
		assets: make(map[string][]byte),
	}
}

// AddAsset 添加单个内存资产。
func (m *MemoryAssetProvider) AddAsset(path string, data []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cleanPath := strings.TrimPrefix(path, "/")
	m.assets[cleanPath] = data
}

func (m *MemoryAssetProvider) Open(name string) (io.ReadCloser, string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cleanPath := strings.TrimPrefix(name, "/")
	if cleanPath == "" {
		cleanPath = "index.html"
	}

	data, ok := m.assets[cleanPath]
	if !ok {
		// SPA 回退
		if indexData, ok2 := m.assets["index.html"]; ok2 {
			return io.NopCloser(strings.NewReader(string(indexData))), "text/html; charset=utf-8", nil
		}
		return nil, "", fmt.Errorf("asset not found: %s", cleanPath)
	}

	ext := filepath.Ext(cleanPath)
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		contentType = detectMimeType(cleanPath)
	}
	return io.NopCloser(strings.NewReader(string(data))), contentType, nil
}

func detectMimeType(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "application/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	case ".woff2":
		return "font/woff2"
	case ".woff":
		return "font/woff"
	case ".ttf":
		return "font/ttf"
	case ".wasm":
		return "application/wasm"
	default:
		return "application/octet-stream"
	}
}

// GlobalAssetRegistry 全局资产路由注册表。
var (
	globalAssetMu sync.RWMutex
	globalAsset   AssetProvider = &MemoryAssetProvider{assets: make(map[string][]byte)}
)

// SetAssetProvider 设置全局前端资产提供者。
func SetAssetProvider(provider AssetProvider) {
	globalAssetMu.Lock()
	defer globalAssetMu.Unlock()
	globalAsset = provider
}

// GetAssetProvider 获取全局资产提供者。
func GetAssetProvider() AssetProvider {
	globalAssetMu.RLock()
	defer globalAssetMu.RUnlock()
	return globalAsset
}

// ResolveAssetURL 解析 aluka://app/* URL 对应的资产流。
func ResolveAssetURL(rawURL string) (io.ReadCloser, string, int, error) {
	p := GetAssetProvider()
	if p == nil {
		return nil, "", http.StatusNotFound, fmt.Errorf("no asset provider registered")
	}

	trimmed := strings.TrimPrefix(rawURL, "aluka://app")
	trimmed = strings.TrimPrefix(trimmed, "aluka://")
	trimmed = strings.TrimPrefix(trimmed, "/")

	rc, mimeType, err := p.Open(trimmed)
	if err != nil {
		return nil, "", http.StatusNotFound, err
	}
	return rc, mimeType, http.StatusOK, nil
}
