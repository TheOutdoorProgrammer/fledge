package web

import _ "embed"

const placeholderSize = 512

//go:embed assets/icon-512.png
var placeholderPNG []byte

// PlaceholderIcon returns the Fledge tile when an uploaded app has no icon.
func PlaceholderIcon() []byte {
	return placeholderPNG
}
