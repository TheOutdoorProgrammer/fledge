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

func TestGeneratedIconAssetsAreValidPNGs(t *testing.T) {
	sizes := map[string]int{
		"icon-1024.png":  1024,
		"icon-512.png":   512,
		"icon-180.png":   180,
		"favicon-32.png": 32,
		"favicon-16.png": 16,
	}

	for name, size := range sizes {
		t.Run(name, func(t *testing.T) {
			raw, err := Asset(name)
			if err != nil {
				t.Fatalf("read asset: %v", err)
			}
			decoded, err := png.Decode(bytes.NewReader(raw))
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if got := decoded.Bounds().Dx(); got != size {
				t.Errorf("width = %d, want %d", got, size)
			}
			if got := decoded.Bounds().Dy(); got != size {
				t.Errorf("height = %d, want %d", got, size)
			}
		})
	}
}
