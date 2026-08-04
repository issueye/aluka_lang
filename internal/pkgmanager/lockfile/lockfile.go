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
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/aluka-lang/aluka/internal/pkgmanager/resolver"
)

// Entry 是 lockfile 中的单包记录。
type Entry struct {
	Name     string
	Version  string
	Resolved string
}

// Lock 是完整 lockfile。
type Lock struct {
	Entries map[string]Entry // key = name
}

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

// Read 读取 lockfile。文件不存在返回 nil（无错误）。
func Read(path string) (*Lock, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()
	l := &Lock{Entries: map[string]Entry{}}
	sc := bufio.NewScanner(f)
	var cur *Entry
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || line == "[lockfile]" {
			continue
		}
		if strings.HasPrefix(line, "[dependencies.") && strings.HasSuffix(line, "]") {
			name := line[len("[dependencies.") : len(line)-1]
			name = strings.ReplaceAll(name, "%2E", ".")
			cur = &Entry{Name: name}
			l.Entries[name] = *cur
			continue
		}
		if cur == nil {
			continue
		}
		if i := strings.IndexByte(line, '='); i > 0 {
			k := strings.TrimSpace(line[:i])
			v := strings.Trim(strings.TrimSpace(line[i+1:]), `"`)
			switch k {
			case "version":
				cur.Version = v
			case "resolved":
				cur.Resolved = v
			}
			l.Entries[cur.Name] = *cur
		}
	}
	return l, sc.Err()
}
