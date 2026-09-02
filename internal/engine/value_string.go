// String 值：扁平字符串与 rope（惰性拼接）两种表示。

package engine

import (
	"strconv"
	"strings"
	"sync/atomic"
	"unicode/utf16"
)

type stringValue string

// ropeStringValue keeps long concatenation chains as a tree. JavaScript
// strings remain immutable; callers that need contiguous bytes flatten the
// tree once through String(), while truthiness and length stay allocation-free.
type ropeStringValue struct {
	left, right Value
	utf16Len    int
	flat        atomic.Pointer[string]
}

const flatConcatLimit = 64

// Str 包装 Go string 为 JS Value。
func Str(s string) Value { return stringValue(s) }

// ConcatStrings applies the existing string coercion rules without repeatedly
// copying an already-growing left operand. Small flat strings stay flat to
// avoid adding rope overhead to ordinary expressions.
func ConcatStrings(left, right Value) Value {
	if left.Type() != TypeString {
		left = Str(left.String())
	}
	if right.Type() != TypeString {
		right = Str(right.String())
	}

	leftLen, _ := StringLen(left)
	rightLen, _ := StringLen(right)
	if leftLen == 0 {
		return right
	}
	if rightLen == 0 {
		return left
	}
	if leftLen+rightLen <= flatConcatLimit {
		if l, lok := left.(stringValue); lok {
			if r, rok := right.(stringValue); rok {
				return stringValue(string(l) + string(r))
			}
		}
	}
	return &ropeStringValue{left: left, right: right, utf16Len: leftLen + rightLen}
}

// StringLen returns the ECMAScript UTF-16 code-unit length. It avoids
// flattening rope strings for repeated `.length` reads.
func StringLen(v Value) (int, bool) {
	if v.Type() != TypeString {
		return 0, false
	}
	if r, ok := v.(*ropeStringValue); ok {
		return r.utf16Len, true
	}
	return len(utf16.Encode([]rune(v.String()))), true
}

func (s stringValue) Type() ValueType { return TypeString }

func (s stringValue) String() string { return string(s) }

func (s stringValue) Int() (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(string(s)))
	if err != nil {
		return 0, false
	}
	return n, true
}

func (s stringValue) Float() (float64, bool) {
	n, err := strconv.ParseFloat(strings.TrimSpace(string(s)), 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func (s stringValue) Bool() (bool, bool) { return string(s) != "", true } // ToBoolean("") = false

func (s stringValue) IsUndefined() bool { return false }

func (s stringValue) IsNull() bool { return false }

func (s stringValue) IsObject() bool { return false }

func (s stringValue) IsFunction() bool { return false }

func (s stringValue) AsObject() (Object, bool) { return nil, false }

func (s stringValue) AsFunction() (Function, bool) { return nil, false }

func (s *ropeStringValue) Type() ValueType { return TypeString }

func (s *ropeStringValue) String() string {
	if flat := s.flat.Load(); flat != nil {
		return *flat
	}

	var b strings.Builder
	b.Grow(s.utf16Len)
	stack := []Value{s}
	for len(stack) > 0 {
		last := len(stack) - 1
		v := stack[last]
		stack = stack[:last]
		if rope, ok := v.(*ropeStringValue); ok {
			if flat := rope.flat.Load(); flat != nil {
				b.WriteString(*flat)
				continue
			}
			stack = append(stack, rope.right, rope.left)
			continue
		}
		b.WriteString(v.String())
	}

	value := b.String()
	s.flat.CompareAndSwap(nil, &value)
	return *s.flat.Load()
}

func (s *ropeStringValue) Int() (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(s.String()))
	return n, err == nil
}

func (s *ropeStringValue) Float() (float64, bool) {
	n, err := strconv.ParseFloat(strings.TrimSpace(s.String()), 64)
	return n, err == nil
}

func (s *ropeStringValue) Bool() (bool, bool) { return s.utf16Len != 0, true }

func (s *ropeStringValue) IsUndefined() bool { return false }

func (s *ropeStringValue) IsNull() bool { return false }

func (s *ropeStringValue) IsObject() bool { return false }

func (s *ropeStringValue) IsFunction() bool { return false }

func (s *ropeStringValue) AsObject() (Object, bool) { return nil, false }

func (s *ropeStringValue) AsFunction() (Function, bool) { return nil, false }
