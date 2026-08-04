// Package registry 实现 npm registry HTTP 客户端（Phase 5 WBS 5.1）。
//
// 支持：
//   - 包元数据获取（versions/dist-tags/dependencies）
//   - tarball 下载（并发由调用方控制）
//   - scoped 包（@scope/pkg）、自定义 registry（镜像/私有/.npmrc 的 registry 字段）
package registry

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultRegistry 是默认 npm registry。
const DefaultRegistry = "https://registry.npmjs.org"

// ErrNotFound 表示包或版本不存在。
var ErrNotFound = errors.New("registry: not found")

// VersionInfo 是单个版本的元数据。
type VersionInfo struct {
	Version            string            `json:"version"`
	Dependencies       map[string]string `json:"dependencies"`
	DevDependencies    map[string]string `json:"devDependencies"`
	PeerDependencies   map[string]string `json:"peerDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	Bin                map[string]string `json:"bin"`
	Dist               DistInfo          `json:"dist"`
}

// DistInfo 描述发布产物（tarball）。
type DistInfo struct {
	Tarball string `json:"tarball"`
}

// Metadata 是包元数据（registry 的 package document）。
type Metadata struct {
	Name     string                  `json:"name"`
	DistTags map[string]string       `json:"dist-tags"`
	Versions map[string]VersionInfo  `json:"versions"`
}

// Client 是 npm registry 客户端。
type Client struct {
	Registry string
	Token    string // 可选鉴权（.npmrc 的 _authToken / token）
	HTTP     *http.Client
}

// New 创建客户端，registry 为空时用默认。
func New(registry string) *Client {
	if registry == "" {
		registry = DefaultRegistry
	}
	return &Client{
		Registry: strings.TrimSuffix(registry, "/"),
		HTTP:     &http.Client{Timeout: 60 * time.Second},
	}
}

// packagePath 构造包元数据 URL（scoped 包需转义 / 为 %2F）。
func packagePath(name string) string {
	if strings.HasPrefix(name, "@") {
		// @scope/name → @scope%2Fname
		if i := strings.IndexByte(name, '/'); i > 0 {
			return url.PathEscape(name[:i+1]) + url.PathEscape(name[i+1:])
		}
	}
	return url.PathEscape(name)
}

// GetMetadata 获取包元数据。
func (c *Client) GetMetadata(name string) (*Metadata, error) {
	u := c.Registry + "/" + packagePath(name)
	body, err := c.get(u)
	if err != nil {
		return nil, err
	}
	var md Metadata
	if err := json.Unmarshal(body, &md); err != nil {
		return nil, fmt.Errorf("registry: parse metadata for %s: %w", name, err)
	}
	return &md, nil
}

// DownloadTarball 下载并返回 tarball 字节。
// URL 相对时基于 registry 解析。
func (c *Client) DownloadTarball(rawURL string) ([]byte, error) {
	if !strings.HasPrefix(rawURL, "http://") && !strings.HasPrefix(rawURL, "https://") {
		rawURL = c.Registry + "/" + strings.TrimPrefix(rawURL, "/")
	}
	body, err := c.get(rawURL)
	if err != nil {
		return nil, fmt.Errorf("registry: download %s: %w", rawURL, err)
	}
	return body, nil
}

// get 发起 GET 并返回响应体（200 时）。
func (c *Client) get(u string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "aluka/0.1.0")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registry: GET %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, u)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry: GET %s: status %d", u, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
