//go:build !amd64 || (!windows && !linux)

package jit

import (
	"github.com/aluka-lang/aluka/internal/engine/jit/native"
)

func compileNativeProgram(*Program, ...bool) (*native.Code, error) { return nil, native.ErrUnsupported }
