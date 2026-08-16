package emit

import (
	"testing"
)

func TestMinifyCSS(t *testing.T) {
	input := `
		/* Main header styling */
		.header, .nav-bar {
			color: #333333 ;
			margin : 10px 20px ;
			background-color : rgba(0, 0, 0, 0.5) ;
		}

		/* Button reset */
		button {
			border: none;
		}
	`
	want := `.header,.nav-bar{color:#333333;margin:10px 20px;background-color:rgba(0,0,0,0.5)}button{border:none}`
	got := MinifyCSS(input)
	if got != want {
		t.Errorf("MinifyCSS mismatch:\n got: %q\nwant: %q", got, want)
	}
}

func TestBundleCSS(t *testing.T) {
	files := []CSSFile{
		{ID: "src/reset.css", Content: "body { margin: 0; }"},
		{ID: "src/style.css", Content: "h1 { color: red; }"},
		{ID: "src/reset.css", Content: "body { margin: 0; }"}, // 重复，应该去重
	}

	bundled, err := BundleCSS(files, true)
	if err != nil {
		t.Fatalf("BundleCSS error: %v", err)
	}

	want := "body{margin:0}h1{color:red}"
	if bundled != want {
		t.Errorf("BundleCSS got %q, want %q", bundled, want)
	}
}
