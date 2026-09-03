// Package config 解析 npm 配置文件（.npmrc）（Phase 5 WBS 5.12）。
//
// 支持 ini 风格：
//
//	registry=https://registry.npmmirror.com
//	//registry.npmjs.org/:_authToken=xxxxx
//	_authToken=fallback-token
//	; 注释行
//	# 注释行
//
// 配置优先级：项目 .npmrc > 用户 ~/.npmrc > 内置默认。
package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Config 是解析后的 .npmrc 配置。
type Config struct {
	// Registry 是默认 registry（不含尾斜杠）。
	Registry string
	// AuthTokens 按 registry 主机（如 registry.npmjs.org）记录鉴权 token。
	AuthTokens map[string]string
	// DefaultToken 是裸 _authToken（不限定主机）。
	DefaultToken string
}

// Load 从项目目录与用户主目录加载 .npmrc（项目优先）。
func Load(rootDir string) (*Config, error) {
	cfg := &Config{AuthTokens: map[string]string{}}
	// 用户级配置先加载（项目级覆盖）。
	if home, err := os.UserHomeDir(); err == nil {
		if user := filepath.Join(home, ".npmrc"); fileExists(user) {
			if err := cfg.MergeFile(user); err != nil {
				return nil, err
			}
		}
	}
	if project := filepath.Join(rootDir, ".npmrc"); fileExists(project) {
		if err := cfg.MergeFile(project); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

// MergeFile 解析并合并一个 .npmrc 文件。
func (c *Config) MergeFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		c.parseLine(line)
	}
	return scanner.Err()
}

// parseLine 解析单行配置。
func (c *Config) parseLine(line string) {
	if line == "" {
		return
	}
	// 注释：; 或 # 开头。
	if line[0] == ';' || line[0] == '#' {
		return
	}
	eq := strings.IndexByte(line, '=')
	if eq < 0 {
		return
	}
	key := strings.TrimSpace(line[:eq])
	value := strings.TrimSpace(line[eq+1:])
	if key == "" {
		return
	}
	// 主机级 token：//host/:_authToken=xxx
	if strings.HasPrefix(key, "//") && strings.HasSuffix(key, ":_authToken") {
		host := strings.TrimSuffix(strings.TrimPrefix(key, "//"), ":_authToken")
		host = strings.TrimSuffix(host, "/")
		if host != "" {
			c.AuthTokens[host] = value
			return
		}
	}
	switch key {
	case "registry":
		if value != "" {
			c.Registry = strings.TrimSuffix(value, "/")
		}
	case "_authToken":
		if value != "" {
			c.DefaultToken = value
		}
	}
}

// TokenFor 返回匹配给定 registry 的鉴权 token。
// 匹配优先级：主机精确匹配 > 前缀匹配 > 裸 _authToken。
func (c *Config) TokenFor(registry string) string {
	host := strings.TrimPrefix(registry, "https://")
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimSuffix(host, "/")
	if tok, ok := c.AuthTokens[host]; ok {
		return tok
	}
	// 前缀匹配：//registry.npmjs.org/:_authToken 应命中 https://registry.npmjs.org
	for h, tok := range c.AuthTokens {
		if strings.HasSuffix(host, "/"+h) || host == h || strings.HasPrefix(host, h+"/") {
			return tok
		}
	}
	return c.DefaultToken
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}
