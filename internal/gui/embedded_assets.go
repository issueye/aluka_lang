// Package gui —— 产物模式（aluka build --gui）内嵌前端资源的运行时挂载。
package gui

import (
	"encoding/base64"
	"fmt"
)

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
	return nil
}
