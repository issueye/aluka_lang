package builtin

import (
	"os"

	"github.com/aluka-lang/aluka/internal/engine"
)

// NewConstants implements the legacy constants/node:constants builtin. The
// module is retained by Node for compatibility; modern code usually reads the
// same values from fs.constants or os.constants.
func NewConstants(ctx engine.Context) (engine.Value, error) {
	m := engine.NewObject()
	values := map[string]int{
		"F_OK":          0,
		"R_OK":          4,
		"W_OK":          2,
		"X_OK":          1,
		"O_RDONLY":      os.O_RDONLY,
		"O_WRONLY":      os.O_WRONLY,
		"O_RDWR":        os.O_RDWR,
		"O_APPEND":      os.O_APPEND,
		"O_CREATE":      os.O_CREATE,
		"O_EXCL":        os.O_EXCL,
		"O_SYNC":        os.O_SYNC,
		"O_TRUNC":       os.O_TRUNC,
		"COPYFILE_EXCL": 1,
	}
	for name, value := range values {
		_ = m.Set(name, engine.IntValue(value))
	}
	return m, nil
}
