package webemit

import (
	"fmt"
	"strings"

	"github.com/aluka-lang/aluka/internal/bundler/emit"
	"github.com/aluka-lang/aluka/internal/bundler/graph"
)

func bundleGraphCSS(gr *graph.Result, baseName string, hashed bool, minifyCSS bool, assetsDir string) (string, string, error) {
	var cssFiles []emit.CSSFile
	for assetKey, data := range gr.Assets {
		if strings.HasSuffix(assetKey, ".css") {
			cssFiles = append(cssFiles, emit.CSSFile{ID: assetKey, Content: string(data)})
		}
	}
	if len(cssFiles) == 0 {
		return "", "", nil
	}
	cssBundle, err := emit.BundleCSS(cssFiles, minifyCSS)
	if err != nil {
		return "", "", fmt.Errorf("bundle CSS: %w", err)
	}
	if cssBundle == "" {
		return "", "", nil
	}
	if hashed {
		return emit.HashedAssetPathIn(assetsDir, baseName, emit.ContentHash(cssBundle), ".css"), cssBundle, nil
	}
	return baseName + ".css", cssBundle, nil
}
