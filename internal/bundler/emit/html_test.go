package emit

import (
	"testing"
)

func TestParseHTMLEntry(t *testing.T) {
	html := `<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <title>Aluka App</title>
    <link rel="stylesheet" href="./style.css">
    <link rel="icon" href="/favicon.ico">
    <link rel="stylesheet" href="https://cdn.example.com/cdn.css">
</head>
<body>
    <div id="root"></div>
    <script type="module" src="./src/main.tsx"></script>
    <script src="https://cdn.example.com/sdk.js"></script>
</body>
</html>`

	assets := ParseHTMLEntry(html)
	if len(assets.Stylesheets) != 1 || assets.Stylesheets[0].Original != "./style.css" {
		t.Errorf("unexpected stylesheets: %+v", assets.Stylesheets)
	}
	if len(assets.Scripts) != 1 || assets.Scripts[0].Original != "./src/main.tsx" {
		t.Errorf("unexpected scripts: %+v", assets.Scripts)
	}
}

func TestRewriteHTML(t *testing.T) {
	html := `<!DOCTYPE html>
<html>
<head>
    <link rel="stylesheet" href="./style.css">
</head>
<body>
    <script type="module" src="./src/main.tsx"></script>
</body>
</html>`

	replacements := map[string]string{
		"./style.css":    "style-a1b2c3d4.css",
		"./src/main.tsx": "main-e5f6g7h8.js",
	}

	rewritten := RewriteHTML(html, replacements)
	want := `<!DOCTYPE html>
<html>
<head>
    <link rel="stylesheet" href="style-a1b2c3d4.css">
</head>
<body>
    <script type="module" src="main-e5f6g7h8.js"></script>
</body>
</html>`

	if rewritten != want {
		t.Errorf("RewriteHTML got:\n%s\nwant:\n%s", rewritten, want)
	}
}
