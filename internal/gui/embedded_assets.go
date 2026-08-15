// Package gui —— 产物模式（aluka build --gui）内嵌前端资源的运行时挂载。
package gui

import (
	"encoding/base64"
	"fmt"
	"sync"
)

// embeddedActive 标记产物模式已挂载内嵌前端资源。
// 此时 setAssetDir 的本地目录覆盖被忽略（打包产物以 --web-dir 内嵌为准，
// 避免开发用相对路径在用户机器上覆盖虚拟协议导致 404）。
var (
	embeddedMu     sync.RWMutex
	embeddedActive bool
)

// EmbeddedAssetsActive 报告是否处于内嵌资源（打包产物）模式。
func EmbeddedAssetsActive() bool {
	embeddedMu.RLock()
	defer embeddedMu.RUnlock()
	return embeddedActive
}

// MountEmbeddedWebAssets 将 base64 编码的内嵌前端资源挂载到
// aluka://app/ 内存虚拟协议（单文件 GUI 产物的默认资产来源）。
// webAssets 与 compile.Manifest.WebAssets 的结构一致：相对路径 → base64。
func MountEmbeddedWebAssets(webAssets map[string]string) error {
	if len(webAssets) == 0 {
		return nil
	}
	provider := NewMemoryAssetProvider()
	for path, b64 := range webAssets {
		data, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return fmt.Errorf("gui: decode embedded asset %q: %w", path, err)
		}
		provider.AddAsset(path, data)
	}
	SetAssetProvider(provider)

	embeddedMu.Lock()
	embeddedActive = true
	embeddedMu.Unlock()
	return nil
}
