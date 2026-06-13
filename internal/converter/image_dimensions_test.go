package converter

import "testing"

func TestParseVipsDimensions(t *testing.T) {
	width, height := parseVipsDimensions([]byte("350\n208\n"))
	if width != 350 || height != 208 {
		t.Fatalf("expected 350x208, got %dx%d", width, height)
	}
}

func TestParseVipsDimensionsInvalid(t *testing.T) {
	width, height := parseVipsDimensions([]byte("not dimensions"))
	if width != 0 || height != 0 {
		t.Fatalf("expected invalid dimensions to parse as zero, got %dx%d", width, height)
	}
}
