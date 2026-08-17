// Package lockfile 实现 aluka.lock 文本 lockfile（Phase 5 WBS 5.6）。
//
// 格式兼容 bun.lock 子集：
//
//	# aluka.lock
//	[lockfile]
//	version = "1"
//
//	[dependencies.lodash]
//	version = "4.17.21"
//	resolved = "https://registry.npmjs.org/lodash/-/lodash-4.17.21.tgz"
//
// 记录解析结果，使后续 install 可在无网络（registry 不可达）时复用。
package lockfile

import (
	"fmt"
	"os"
	"strings"

	"github.com/aluka-lang/aluka/internal/pkgmanager/resolver"
)

// Write 把解析结果写入 lockfile。
func Write(path string, res *resolver.Resolution) error {
	var sb strings.Builder
	sb.WriteString("# aluka.lock (v1, bun.lock compatible)\n")
	sb.WriteString("[lockfile]\n")
	sb.WriteString("version = \"1\"\n")
	for _, pkg := range res.PkgOrder() {
		name := strings.ReplaceAll(pkg.Name, ".", "%2E")
		fmt.Fprintf(&sb, "\n[dependencies.%s]\n", name)
		fmt.Fprintf(&sb, "version = %q\n", pkg.Version)
		fmt.Fprintf(&sb, "resolved = %q\n", pkg.Tarball)
	}
	return os.WriteFile(path, []byte(sb.String()), 0o644)
}
