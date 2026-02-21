//go:build integration

package integration

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"testing"
)

// MP4 ftyp box: bytes 4-7 must be "ftyp"
func assertMP4(t *testing.T, data []byte) {
	t.Helper()
	if len(data) < 12 {
		t.Fatalf("output too small to be MP4: %d bytes", len(data))
	}
	if string(data[4:8]) != "ftyp" {
		t.Fatalf("output is not MP4: missing ftyp box (got %x)", data[4:8])
	}
}

func TestMovToMp4_RealFile(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample_640x360.mov")

	sub := submitFile(t, server.URL, "/api/convert/video/mov-to-mp4", "video.mov", data, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("MOV->MP4 failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	assertMP4(t, result)
	if len(result) < 1000 {
		t.Fatalf("output suspiciously small: %d bytes", len(result))
	}
	t.Logf("MOV->MP4 (640x360): %d bytes -> %d bytes", len(data), len(result))
}

func TestMovToMp4_SmallH264(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample_small.mov")

	sub := submitFile(t, server.URL, "/api/convert/video/mov-to-mp4", "clip.mov", data, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("MOV->MP4 (small H.264) failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	assertMP4(t, result)
	t.Logf("MOV->MP4 (small H.264+AAC): %d bytes -> %d bytes", len(data), len(result))
}

func TestMovToMp4_MPEG4Codec(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample_mpeg4.mov")

	sub := submitFile(t, server.URL, "/api/convert/video/mov-to-mp4", "legacy.mov", data, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("MOV->MP4 (MPEG-4) failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	assertMP4(t, result)
	t.Logf("MOV->MP4 (MPEG-4+PCM): %d bytes -> %d bytes", len(data), len(result))
}

func TestMovToMp4_VideoOnly(t *testing.T) {
	server, _, _ := setupTestServer(t)
	data := loadTestFile(t, "sample_noaudio.mov")

	sub := submitFile(t, server.URL, "/api/convert/video/mov-to-mp4", "noaudio.mov", data, nil)
	status := waitDone(t, server.URL, sub.JobID)
	if status.Status != "done" {
		t.Fatalf("MOV->MP4 (no audio) failed: %s", status.Error)
	}

	result := downloadResult(t, server.URL, status.DownloadURL)
	assertMP4(t, result)
	t.Logf("MOV->MP4 (video only): %d bytes -> %d bytes", len(data), len(result))
}

func TestMovToMp4_RejectsNonMOV(t *testing.T) {
	server, _, _ := setupTestServer(t)
	// Submit a JPEG file to the MOV endpoint — should be rejected
	data := loadTestFile(t, "sample.jpg")

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "fake.mov")
	if err != nil {
		t.Fatal(err)
	}
	part.Write(data)
	writer.Close()

	resp, err := http.Post(server.URL+"/api/convert/video/mov-to-mp4", writer.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Should get 400 (invalid file type) not 202
	if resp.StatusCode == http.StatusAccepted {
		t.Fatal("expected rejection for non-MOV file, but got 202 Accepted")
	}
	t.Logf("Non-MOV correctly rejected with status %d", resp.StatusCode)
}
