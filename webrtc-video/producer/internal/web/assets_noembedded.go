//go:build noembeddedweb

package web

import "errors"

var errEmbeddedViewerUnavailable = errors.New("embedded viewer UI is not available in this binary")

func EmbeddedViewerAvailable() bool {
	return false
}

func readEmbeddedAsset(string) ([]byte, error) {
	return nil, errEmbeddedViewerUnavailable
}
