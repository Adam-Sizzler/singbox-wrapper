//go:build windows

package app

import (
	"fmt"
	"strings"
)

const (
	uiStylesTagPrefix = `<link rel="stylesheet" href="/`
	uiStylesTagSuffix = `">`
	uiScriptTagPrefix = `<script src="/`
	uiScriptTagSuffix = `"></script>`
)

var uiStyleFiles = []string{
	"theme.css",
	"styles.css",
	"traffic.css",
}

var uiScriptFiles = []string{
	"traffic.js",
	"app.js",
}

func loadEmbeddedUIHTML() (string, error) {
	indexBytes, err := uiAssets.ReadFile("web/ui/index.html")
	if err != nil {
		return "", fmt.Errorf("read index.html: %w", err)
	}
	html := string(indexBytes)
	for _, fileName := range uiStyleFiles {
		tag := uiStylesTagPrefix + fileName + uiStylesTagSuffix
		if !strings.Contains(html, tag) {
			return "", fmt.Errorf("%s tag not found in index.html", fileName)
		}
		fileBytes, err := uiAssets.ReadFile("web/ui/" + fileName)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", fileName, err)
		}
		html = strings.Replace(html, tag, "<style>\n"+string(fileBytes)+"\n</style>", 1)
	}

	for _, fileName := range uiScriptFiles {
		tag := uiScriptTagPrefix + fileName + uiScriptTagSuffix
		if !strings.Contains(html, tag) {
			return "", fmt.Errorf("%s tag not found in index.html", fileName)
		}
		fileBytes, err := uiAssets.ReadFile("web/ui/" + fileName)
		if err != nil {
			return "", fmt.Errorf("read %s: %w", fileName, err)
		}
		html = strings.Replace(html, tag, "<script>\n"+string(fileBytes)+"\n</script>", 1)
	}
	return html, nil
}
