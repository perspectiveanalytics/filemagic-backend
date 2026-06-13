package converter

import "testing"

func TestRedactCommandOnlyRedacts7zPasswordSwitch(t *testing.T) {
	got := redactCommand([]string{"/usr/bin/7z", "x", "-psecret", "archive.zip"})
	if got[2] != "-p[REDACTED]" {
		t.Fatalf("expected 7z password to be redacted, got %q", got[2])
	}
}

func TestRedactCommandDoesNotRedactFFmpegProtocolWhitelist(t *testing.T) {
	got := redactCommand([]string{"/usr/bin/ffmpeg", "-protocol_whitelist", "file,pipe"})
	if got[1] != "-protocol_whitelist" {
		t.Fatalf("expected ffmpeg option to stay visible, got %q", got[1])
	}
}

func TestStdoutLimitForConfig(t *testing.T) {
	if got := stdoutLimitForConfig("archive.cfg"); got != largeNsjailStdoutBytes {
		t.Fatalf("expected large archive stdout limit, got %d", got)
	}
	if got := stdoutLimitForConfig("metadata.cfg"); got != largeNsjailStdoutBytes {
		t.Fatalf("expected large metadata stdout limit, got %d", got)
	}
	if got := stdoutLimitForConfig("image.cfg"); got != defaultNsjailStdoutBytes {
		t.Fatalf("expected default image stdout limit, got %d", got)
	}
}

func TestLimitedBufferCapsStoredBytes(t *testing.T) {
	buf := newLimitedBuffer(3)
	n, err := buf.Write([]byte("abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 6 {
		t.Fatalf("expected full write length, got %d", n)
	}
	if string(buf.Bytes()) != "abc" {
		t.Fatalf("expected capped bytes, got %q", string(buf.Bytes()))
	}
	if !buf.Truncated() {
		t.Fatal("expected buffer to report truncation")
	}
}
