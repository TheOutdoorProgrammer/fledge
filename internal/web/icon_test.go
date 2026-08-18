package web

import (
	"bytes"
	"image/png"
	"os"
	"testing"
)

func TestPlaceholderIconIsAValidPNG(t *testing.T) {
	raw := PlaceholderIcon()
	if len(raw) == 0 {
		t.Fatal("placeholder icon is empty")
	}

	decoded, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}

	bounds := decoded.Bounds()
	if bounds.Dx() != placeholderSize || bounds.Dy() != placeholderSize {
		t.Errorf("size = %dx%d, want %dx%d", bounds.Dx(), bounds.Dy(), placeholderSize, placeholderSize)
	}

	if _, _, _, alpha := decoded.At(0, 0).RGBA(); alpha != 0 {
		t.Errorf("corner alpha = %d, want a rounded (transparent) corner", alpha)
	}
	if _, _, _, alpha := decoded.At(placeholderSize/2, placeholderSize/2).RGBA(); alpha == 0 {
		t.Error("centre is transparent")
	}

	if out := os.Getenv("FLEDGE_TEST_ICON_OUT"); out != "" {
		if err := os.WriteFile(out, raw, 0o600); err != nil {
			t.Fatalf("write icon: %v", err)
		}
	}
}
