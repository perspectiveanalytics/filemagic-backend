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

func TestParseVipsDimensionsSingleField(t *testing.T) {
	width, height := parseVipsDimensions([]byte("350\n"))
	if width != 350 || height != 0 {
		t.Fatalf("expected partial dimensions to parse as 350x0, got %dx%d", width, height)
	}
}

func TestParseVipsDimensionField(t *testing.T) {
	value := parseVipsDimensionField([]byte("208\n"))
	if value != 208 {
		t.Fatalf("expected 208, got %d", value)
	}
}

func TestParseVipsDimensionFieldInvalid(t *testing.T) {
	value := parseVipsDimensionField([]byte("height: 208"))
	if value != 0 {
		t.Fatalf("expected invalid field to parse as zero, got %d", value)
	}
}
