package gui

import (
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestNormalizeDialogOptions(t *testing.T) {
	got := NormalizeDialogOptions(DialogOptions{
		Title:      "pick",
		Properties: []string{"openDirectory", "multiSelections"},
	})
	if !got.Directory || !got.Multiple {
		t.Fatalf("properties not applied: %+v", got)
	}
	if got.Type != "openFile" {
		t.Fatalf("type = %q, want openFile", got.Type)
	}

	save := NormalizeDialogOptions(DialogOptions{Type: "save"})
	if save.Type != "saveFile" {
		t.Fatalf("save type = %q", save.Type)
	}
}

func TestWin32FilterString(t *testing.T) {
	s := win32FilterString([]FileFilter{{Name: "Text", Extensions: []string{"txt", ".md"}}})
	if !strings.Contains(s, "Text") || !strings.Contains(s, "*.txt;*.md") {
		t.Fatalf("filter %q missing expected entries", s)
	}
	if !strings.HasSuffix(s, "\x00") {
		t.Fatalf("filter should end with NUL")
	}
}

func TestParseNULSeparatedPaths(t *testing.T) {
	single := utf16.Encode([]rune("C:\\a.txt"))
	single = append(single, 0, 0)
	got := parseNULSeparatedPaths(single)
	if len(got) != 1 || got[0] != `C:\a.txt` {
		t.Fatalf("single = %#v", got)
	}

	multi := utf16.Encode([]rune("C:\\docs\x00a.txt\x00b.txt"))
	multi = append(multi, 0, 0)
	got = parseNULSeparatedPaths(multi)
	want0 := filepath.Join(`C:\docs`, "a.txt")
	want1 := filepath.Join(`C:\docs`, "b.txt")
	if len(got) != 2 || got[0] != want0 || got[1] != want1 {
		t.Fatalf("multi = %#v want %q %q", got, want0, want1)
	}
}

func TestMessageBoxStyle(t *testing.T) {
	flags, mapRet := messageBoxStyle(DialogOptions{Type: "error", Buttons: []string{"Yes", "No"}})
	if flags&0x00000010 == 0 {
		t.Fatalf("expected error icon, flags=0x%x", flags)
	}
	if mapRet(6) != 0 || mapRet(7) != 1 {
		t.Fatalf("Yes/No mapping failed")
	}
}
