//go:build !noembeddedweb

package web

import "embed"

//go:embed embed/index.html generated/*
var embeddedAssets embed.FS

func EmbeddedViewerAvailable() bool {
	return true
}

func readEmbeddedAsset(name string) ([]byte, error) {
	return embeddedAssets.ReadFile(name)
}
