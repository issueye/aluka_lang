package gui

import "testing"

func TestParseAccelerator(t *testing.T) {
	cases := []struct {
		in      string
		mods    Modifiers
		key     string
		wantErr bool
	}{
		{"Ctrl+Shift+P", ModCtrl | ModShift, "P", false},
		{"ctrl+shift+p", ModCtrl | ModShift, "P", false},
		{"Alt+F4", ModAlt, "F4", false},
		{"CommandOrControl+K", ModCtrl, "K", false},
		{"Super+D", ModSuper, "D", false},
		{"Shift+7", ModShift, "7", false},
		{"Ctrl+Alt+Delete", ModCtrl | ModAlt, "DELETE", false},
		{"Escape", ModNone, "ESCAPE", false},
		{"F5", ModNone, "F5", false},
		{"Ctrl+ArrowUp", 0, "", true},  // 未知修饰键
		{"Ctrl+", 0, "", true},         // 缺主键
		{"Ctrl+Home+End", 0, "", true}, // 多余主键
		{"", 0, "", true},
		{"Ctrl+F13", 0, "", true}, // 不支持的键
	}

	for _, c := range cases {
		got, err := ParseAccelerator(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("ParseAccelerator(%q): expected error, got %+v", c.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseAccelerator(%q): unexpected error: %v", c.in, err)
			continue
		}
		if got.Modifier != c.mods || got.Key != c.key {
			t.Errorf("ParseAccelerator(%q) = (mods=%b, key=%q), want (mods=%b, key=%q)",
				c.in, got.Modifier, got.Key, c.mods, c.key)
		}
	}
}
