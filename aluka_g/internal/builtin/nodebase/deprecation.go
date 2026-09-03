// Node 废弃警告输出（DEP00xx）。

package nodebase

// 废弃 API 的 DeprecationWarning 输出（Node 风格）。

import (
	"fmt"
	"os"
)

// emitDeprecation 向 stderr 输出一次 DeprecationWarning。
// 格式对齐 Node：`(node:pid) [CODE] DeprecationWarning: message`。
func EmitDeprecation(code, message string) {
	fmt.Fprintf(os.Stderr, "(aluka) [%s] DeprecationWarning: %s\n", code, message)
}
