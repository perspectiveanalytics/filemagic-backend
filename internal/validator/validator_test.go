package validator

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// helpers

// makeHeader builds a 32-byte header with the given prefix (rest zero-filled).
func makeHeader(data []byte) []byte {
	buf := make([]byte, 32)
	copy(buf, data)
	return buf
}

// makeFtyp builds a valid ISO BMFF ftyp header.
// boxSize is the ftyp box size, brand is the 4-byte major brand.
func makeFtyp(boxSize uint32, brand string) []byte {
	buf := make([]byte, 32)
	binary.BigEndian.PutUint32(buf[0:4], boxSize)
	copy(buf[4:8], "ftyp")
	copy(buf[8:12], brand)
	return buf
}

// makeRIFF builds a RIFF header with the given chunk size and form type.
func makeRIFF(chunkSize uint32, formType string) []byte {
	buf := make([]byte, 32)
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], chunkSize)
	copy(buf[8:12], formType)
	return buf
}

// makeMP3Frame builds a valid 4-byte MPEG1 Layer3 frame header.
// sync=0xFF, version/layer/bitrate/sample encoded in bytes 1-3.
func makeMP3Frame() []byte {
	// FF FB 90 00: MPEG1, Layer3, 128kbps, 44100Hz
	return []byte{0xFF, 0xFB, 0x90, 0x00}
}

// DetectFileType: valid files

func TestDetectFileType_JPEG(t *testing.T) {
	// Valid JPEG: FF D8 FF E0 (SOI + APP0)
	data := makeHeader([]byte{0xFF, 0xD8, 0xFF, 0xE0})
	ft, err := DetectFileType(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft != FileTypeJPEG {
		t.Fatalf("expected jpeg, got %s", ft)
	}
}

func TestDetectFileType_JPEG_APP1(t *testing.T) {
	// EXIF JPEG: FF D8 FF E1
	data := makeHeader([]byte{0xFF, 0xD8, 0xFF, 0xE1})
	ft, err := DetectFileType(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft != FileTypeJPEG {
		t.Fatalf("expected jpeg, got %s", ft)
	}
}

func TestDetectFileType_JPEG_DQT(t *testing.T) {
	// JPEG starting with DQT marker: FF D8 FF DB
	data := makeHeader([]byte{0xFF, 0xD8, 0xFF, 0xDB})
	ft, err := DetectFileType(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft != FileTypeJPEG {
		t.Fatalf("expected jpeg, got %s", ft)
	}
}

func TestDetectFileType_PNG(t *testing.T) {
	// Valid PNG with IHDR
	data := make([]byte, 32)
	copy(data[0:8], []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})
	binary.BigEndian.PutUint32(data[8:12], 13)   // IHDR chunk length
	copy(data[12:16], "IHDR")                     // chunk type
	data[16] = 0x00; data[17] = 0x00; data[18] = 0x01; data[19] = 0x00 // width
	data[20] = 0x00; data[21] = 0x00; data[22] = 0x01; data[23] = 0x00 // height

	ft, err := DetectFileType(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft != FileTypePNG {
		t.Fatalf("expected png, got %s", ft)
	}
}

func TestDetectFileType_BMP(t *testing.T) {
	// Valid BMP: BM + file size + reserved (0000 0000)
	data := make([]byte, 32)
	data[0] = 0x42; data[1] = 0x4D // BM
	binary.LittleEndian.PutUint32(data[2:6], 1024) // file size
	// bytes 6-9 reserved = 0 (already zero)
	binary.LittleEndian.PutUint32(data[10:14], 54) // pixel data offset
	binary.LittleEndian.PutUint32(data[14:18], 40) // BITMAPINFOHEADER size

	ft, err := DetectFileType(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft != FileTypeBMP {
		t.Fatalf("expected bmp, got %s", ft)
	}
}

func TestDetectFileType_TIFF_LE(t *testing.T) {
	data := makeHeader([]byte{0x49, 0x49, 0x2A, 0x00})
	ft, err := DetectFileType(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft != FileTypeTIFF {
		t.Fatalf("expected tiff, got %s", ft)
	}
}

func TestDetectFileType_TIFF_BE(t *testing.T) {
	data := makeHeader([]byte{0x4D, 0x4D, 0x00, 0x2A})
	ft, err := DetectFileType(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft != FileTypeTIFF {
		t.Fatalf("expected tiff, got %s", ft)
	}
}

func TestDetectFileType_PDF(t *testing.T) {
	data := makeHeader([]byte("%PDF-1.4"))
	ft, err := DetectFileType(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft != FileTypePDF {
		t.Fatalf("expected pdf, got %s", ft)
	}
}

func TestDetectFileType_FLAC(t *testing.T) {
	data := makeHeader([]byte("fLaC"))
	ft, err := DetectFileType(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft != FileTypeFLAC {
		t.Fatalf("expected flac, got %s", ft)
	}
}

func TestDetectFileType_MP3_ID3(t *testing.T) {
	data := makeHeader([]byte("ID3\x04\x00\x00"))
	ft, err := DetectFileType(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft != FileTypeMP3 {
		t.Fatalf("expected mp3, got %s", ft)
	}
}

func TestDetectFileType_MP3_SyncFrame(t *testing.T) {
	data := makeHeader(makeMP3Frame())
	ft, err := DetectFileType(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft != FileTypeMP3 {
		t.Fatalf("expected mp3, got %s", ft)
	}
}

func TestDetectFileType_WAV(t *testing.T) {
	data := makeRIFF(1000, "WAVE")
	ft, err := DetectFileType(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft != FileTypeWAV {
		t.Fatalf("expected wav, got %s", ft)
	}
}

func TestDetectFileType_WEBP(t *testing.T) {
	data := makeRIFF(1000, "WEBP")
	ft, err := DetectFileType(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft != FileTypeWEBP {
		t.Fatalf("expected webp, got %s", ft)
	}
}

func TestDetectFileType_AVI(t *testing.T) {
	data := makeRIFF(1000, "AVI ")
	ft, err := DetectFileType(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft != FileTypeAVI {
		t.Fatalf("expected avi, got %s", ft)
	}
}

func TestDetectFileType_MKV(t *testing.T) {
	data := makeHeader([]byte{0x1A, 0x45, 0xDF, 0xA3})
	ft, err := DetectFileType(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft != FileTypeMKV {
		t.Fatalf("expected mkv, got %s", ft)
	}
}

func TestDetectFileType_MP4_isom(t *testing.T) {
	data := makeFtyp(20, "isom")
	ft, err := DetectFileType(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft != FileTypeMP4 {
		t.Fatalf("expected mp4, got %s", ft)
	}
}

func TestDetectFileType_MP4_avc1(t *testing.T) {
	data := makeFtyp(28, "avc1")
	ft, err := DetectFileType(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft != FileTypeMP4 {
		t.Fatalf("expected mp4, got %s", ft)
	}
}

func TestDetectFileType_MOV(t *testing.T) {
	data := makeFtyp(20, "qt  ")
	ft, err := DetectFileType(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft != FileTypeMOV {
		t.Fatalf("expected mov, got %s", ft)
	}
}

func TestDetectFileType_M4A(t *testing.T) {
	data := makeFtyp(20, "M4A ")
	ft, err := DetectFileType(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft != FileTypeM4A {
		t.Fatalf("expected m4a, got %s", ft)
	}
}

func TestDetectFileType_HEIC(t *testing.T) {
	data := makeFtyp(20, "heic")
	ft, err := DetectFileType(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft != FileTypeHEIC {
		t.Fatalf("expected heic, got %s", ft)
	}
}

func TestDetectFileType_HEIC_mif1(t *testing.T) {
	data := makeFtyp(20, "mif1")
	ft, err := DetectFileType(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ft != FileTypeHEIC {
		t.Fatalf("expected heic, got %s", ft)
	}
}

// DetectFileType: rejection cases

func TestDetectFileType_EmptyFile(t *testing.T) {
	_, err := DetectFileType(bytes.NewReader(nil))
	if err != ErrUnknownFileType {
		t.Fatalf("expected ErrUnknownFileType, got %v", err)
	}
}

func TestDetectFileType_RandomBytes(t *testing.T) {
	data := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F}
	_, err := DetectFileType(bytes.NewReader(data))
	if err != ErrUnknownFileType {
		t.Fatalf("expected ErrUnknownFileType, got %v", err)
	}
}

func TestDetectFileType_TooShort(t *testing.T) {
	// Single byte — not enough for any pattern
	_, err := DetectFileType(bytes.NewReader([]byte{0xFF}))
	if err != ErrUnknownFileType {
		t.Fatalf("expected ErrUnknownFileType, got %v", err)
	}
}

func TestDetectFileType_EXE(t *testing.T) {
	// PE executable: MZ header
	data := makeHeader([]byte("MZ"))
	_, err := DetectFileType(bytes.NewReader(data))
	if err != ErrUnknownFileType {
		t.Fatalf("expected ErrUnknownFileType for EXE, got %v", err)
	}
}

func TestDetectFileType_ELF(t *testing.T) {
	data := makeHeader([]byte{0x7F, 0x45, 0x4C, 0x46})
	_, err := DetectFileType(bytes.NewReader(data))
	if err != ErrUnknownFileType {
		t.Fatalf("expected ErrUnknownFileType for ELF, got %v", err)
	}
}

func TestDetectFileType_ShellScript(t *testing.T) {
	data := makeHeader([]byte("#!/bin/sh\n"))
	_, err := DetectFileType(bytes.NewReader(data))
	if err != ErrUnknownFileType {
		t.Fatalf("expected ErrUnknownFileType for shell script, got %v", err)
	}
}

// Structural validation bypass tests

func TestReject_JPEG_InvalidMarker(t *testing.T) {
	// FF D8 FF 00 — 00 is not a valid JPEG marker after SOI
	data := makeHeader([]byte{0xFF, 0xD8, 0xFF, 0x00})
	_, err := DetectFileType(bytes.NewReader(data))
	if err != ErrUnknownFileType {
		t.Fatalf("expected rejection of JPEG with invalid marker byte 0x00, got no error")
	}
}

func TestReject_JPEG_TruncatedHeader(t *testing.T) {
	// Only 3 bytes — no room for marker type
	_, err := DetectFileType(bytes.NewReader([]byte{0xFF, 0xD8, 0xFF}))
	if err != ErrUnknownFileType {
		t.Fatalf("expected rejection of truncated JPEG, got no error")
	}
}

func TestReject_PNG_MissingIHDR(t *testing.T) {
	// Valid PNG signature but no IHDR chunk
	data := make([]byte, 32)
	copy(data[0:8], []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})
	copy(data[12:16], "XXXX") // not IHDR

	_, err := DetectFileType(bytes.NewReader(data))
	if err != ErrUnknownFileType {
		t.Fatalf("expected rejection of PNG without IHDR, got no error")
	}
}

func TestReject_PNG_TruncatedHeader(t *testing.T) {
	// Only 8 bytes (signature but no IHDR)
	data := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	_, err := DetectFileType(bytes.NewReader(data))
	if err != ErrUnknownFileType {
		t.Fatalf("expected rejection of truncated PNG, got no error")
	}
}

func TestReject_BMP_NonZeroReserved(t *testing.T) {
	// BM header but reserved bytes are non-zero (likely not a real BMP)
	data := make([]byte, 32)
	data[0] = 0x42; data[1] = 0x4D
	data[6] = 0x01 // reserved field non-zero

	_, err := DetectFileType(bytes.NewReader(data))
	if err != ErrUnknownFileType {
		t.Fatalf("expected rejection of BMP with non-zero reserved fields, got no error")
	}
}

func TestReject_MP3Sync_ReservedVersion(t *testing.T) {
	// FF F9 xx xx — MPEG version 01 (reserved)
	// byte1: 1111 1 01 1 → version=01 (reserved), layer=11 (Layer1)
	data := makeHeader([]byte{0xFF, 0xF9, 0x90, 0x00})
	_, err := DetectFileType(bytes.NewReader(data))
	if err != ErrUnknownFileType {
		t.Fatalf("expected rejection of MP3 sync with reserved MPEG version, got no error")
	}
}

func TestReject_MP3Sync_ReservedLayer(t *testing.T) {
	// FF FF xx xx — layer=00 (reserved)
	// byte1: 1111 1 11 1 → version=11 (MPEG1), layer=00 (reserved), CRC=1
	data := makeHeader([]byte{0xFF, 0xFF, 0x90, 0x00})
	_, err := DetectFileType(bytes.NewReader(data))
	if err != ErrUnknownFileType {
		t.Fatalf("expected rejection of MP3 sync with reserved layer, got no error")
	}
}

func TestReject_MP3Sync_InvalidBitrate(t *testing.T) {
	// byte2: bitrate index 1111 (invalid)
	// FF FB F0 00 → MPEG1 Layer3, bitrate=1111(bad), sample=00(44100)
	data := makeHeader([]byte{0xFF, 0xFB, 0xF0, 0x00})
	_, err := DetectFileType(bytes.NewReader(data))
	if err != ErrUnknownFileType {
		t.Fatalf("expected rejection of MP3 sync with invalid bitrate index, got no error")
	}
}

func TestReject_MP3Sync_InvalidSampleRate(t *testing.T) {
	// byte2: sample rate index 11 (reserved)
	// FF FB 9C 00 → MPEG1 Layer3, bitrate=1001(128k), sample=11(reserved)
	data := makeHeader([]byte{0xFF, 0xFB, 0x9C, 0x00})
	_, err := DetectFileType(bytes.NewReader(data))
	if err != ErrUnknownFileType {
		t.Fatalf("expected rejection of MP3 sync with reserved sample rate, got no error")
	}
}

func TestReject_MP3Sync_RandomFalsePositive(t *testing.T) {
	// FF FB 00 00 — matches 2-byte sync but bitrate=0000 (free format),
	// sample=00(44100), layer=11, version=11 → this is actually valid per spec
	// (free format bitrate). So this should pass.
	// Use a truly invalid combo instead: FF F2 FE 00
	// byte1: 1111 0 01 0 → version=01 (reserved)
	data := makeHeader([]byte{0xFF, 0xF2, 0xFE, 0x00})
	_, err := DetectFileType(bytes.NewReader(data))
	if err != ErrUnknownFileType {
		t.Fatalf("expected rejection of FF F2 with reserved MPEG version, got no error")
	}
}

func TestReject_FtypBoxSizeZero(t *testing.T) {
	// ftyp box with size=0 (extends to EOF — suspicious for file type detection)
	data := makeFtyp(0, "isom")
	_, err := DetectFileType(bytes.NewReader(data))
	if err != ErrUnknownFileType {
		t.Fatalf("expected rejection of ftyp with boxSize=0, got no error")
	}
}

func TestReject_FtypBoxSizeTooSmall(t *testing.T) {
	// ftyp box with size=4 (less than minimum 8)
	data := makeFtyp(4, "isom")
	_, err := DetectFileType(bytes.NewReader(data))
	if err != ErrUnknownFileType {
		t.Fatalf("expected rejection of ftyp with boxSize=4, got no error")
	}
}

func TestReject_FtypBoxSizeHuge(t *testing.T) {
	// ftyp box with unreasonably large size (>1024)
	data := makeFtyp(999999, "isom")
	_, err := DetectFileType(bytes.NewReader(data))
	if err != ErrUnknownFileType {
		t.Fatalf("expected rejection of ftyp with huge boxSize, got no error")
	}
}

func TestReject_RIFFChunkSizeZero(t *testing.T) {
	data := makeRIFF(0, "WAVE")
	_, err := DetectFileType(bytes.NewReader(data))
	if err != ErrUnknownFileType {
		t.Fatalf("expected rejection of RIFF with chunkSize=0, got no error")
	}
}

func TestReject_RIFFChunkSizeMax(t *testing.T) {
	data := makeRIFF(0xFFFFFFFF, "WAVE")
	_, err := DetectFileType(bytes.NewReader(data))
	if err != ErrUnknownFileType {
		t.Fatalf("expected rejection of RIFF with chunkSize=0xFFFFFFFF, got no error")
	}
}

func TestReject_RIFFUnknownSubtype(t *testing.T) {
	// RIFF with unknown subtype should be rejected
	data := makeRIFF(1000, "XXXX")
	_, err := DetectFileType(bytes.NewReader(data))
	if err != ErrUnknownFileType {
		t.Fatalf("expected rejection of RIFF with unknown subtype, got no error")
	}
}

func TestReject_UnknownFtypBrand(t *testing.T) {
	// ftyp with unknown brand should be rejected
	data := makeFtyp(20, "XXXX")
	_, err := DetectFileType(bytes.NewReader(data))
	if err != ErrUnknownFileType {
		t.Fatalf("expected rejection of ftyp with unknown brand, got no error")
	}
}

// Polyglot / attack scenario tests

func TestReject_BMSpoof_NonZeroReserved(t *testing.T) {
	// Attacker prepends BM to a payload but doesn't zero the reserved fields
	data := make([]byte, 32)
	data[0] = 0x42; data[1] = 0x4D
	// Fill with "random" payload data (non-zero in reserved fields)
	for i := 2; i < 32; i++ {
		data[i] = byte(i)
	}

	_, err := DetectFileType(bytes.NewReader(data))
	if err != ErrUnknownFileType {
		t.Fatalf("expected rejection of BM-spoofed file with non-zero reserved, got no error")
	}
}

func TestReject_JPEGSpoof_BadMarker(t *testing.T) {
	// Attacker prepends FF D8 FF to arbitrary data, but byte 3 = 0x01 (invalid marker)
	data := makeHeader([]byte{0xFF, 0xD8, 0xFF, 0x01, 0x00, 0x00})
	_, err := DetectFileType(bytes.NewReader(data))
	if err != ErrUnknownFileType {
		t.Fatalf("expected rejection of JPEG-spoofed file with invalid marker, got no error")
	}
}

// Validate functions

func TestValidateForImageConversion(t *testing.T) {
	valid := []FileType{FileTypeJPEG, FileTypePNG, FileTypeWEBP, FileTypeHEIC, FileTypeBMP, FileTypeTIFF}
	for _, ft := range valid {
		if !ValidateForImageConversion(ft) {
			t.Errorf("expected %s to be valid for image conversion", ft)
		}
	}
	invalid := []FileType{FileTypePDF, FileTypeMP4, FileTypeMP3, FileTypeWAV, FileTypeMKV}
	for _, ft := range invalid {
		if ValidateForImageConversion(ft) {
			t.Errorf("expected %s to be invalid for image conversion", ft)
		}
	}
}

func TestValidateForAudioExtract(t *testing.T) {
	valid := []FileType{FileTypeMP4, FileTypeMOV, FileTypeMKV, FileTypeAVI}
	for _, ft := range valid {
		if !ValidateForAudioExtract(ft) {
			t.Errorf("expected %s to be valid for audio extraction", ft)
		}
	}
	invalid := []FileType{FileTypeMP3, FileTypeWAV, FileTypePDF, FileTypePNG}
	for _, ft := range invalid {
		if ValidateForAudioExtract(ft) {
			t.Errorf("expected %s to be invalid for audio extraction", ft)
		}
	}
}

func TestValidateForAudioConvert(t *testing.T) {
	valid := []FileType{FileTypeMP3, FileTypeWAV, FileTypeFLAC, FileTypeM4A, FileTypeMP4}
	for _, ft := range valid {
		if !ValidateForAudioConvert(ft) {
			t.Errorf("expected %s to be valid for audio conversion", ft)
		}
	}
	invalid := []FileType{FileTypePDF, FileTypePNG, FileTypeAVI}
	for _, ft := range invalid {
		if ValidateForAudioConvert(ft) {
			t.Errorf("expected %s to be invalid for audio conversion", ft)
		}
	}
}

func TestValidateForVideoCompress(t *testing.T) {
	valid := []FileType{FileTypeMP4, FileTypeMOV, FileTypeMKV, FileTypeAVI}
	for _, ft := range valid {
		if !ValidateForVideoCompress(ft) {
			t.Errorf("expected %s to be valid for video compression", ft)
		}
	}
	invalid := []FileType{FileTypeMP3, FileTypePDF, FileTypePNG}
	for _, ft := range invalid {
		if ValidateForVideoCompress(ft) {
			t.Errorf("expected %s to be invalid for video compression", ft)
		}
	}
}

func TestValidateForMovToMp4(t *testing.T) {
	if !ValidateForMovToMp4(FileTypeMOV) {
		t.Error("expected MOV to be valid")
	}
	if ValidateForMovToMp4(FileTypeMP4) {
		t.Error("expected MP4 to be invalid for MOV-to-MP4")
	}
}

func TestValidateForPDFPassword(t *testing.T) {
	if !ValidateForPDFPassword(FileTypePDF) {
		t.Error("expected PDF to be valid")
	}
	if ValidateForPDFPassword(FileTypePNG) {
		t.Error("expected PNG to be invalid for PDF password")
	}
}

// GetExtension / GetMimeType

func TestGetExtension(t *testing.T) {
	tests := map[FileType]string{
		FileTypeJPEG: ".jpg",
		FileTypePNG:  ".png",
		FileTypeWEBP: ".webp",
		FileTypePDF:  ".pdf",
		FileTypeMP4:  ".mp4",
		FileTypeMP3:  ".mp3",
		FileTypeWAV:  ".wav",
		FileTypeFLAC: ".flac",
		FileTypeM4A:  ".m4a",
		FileTypeMKV:  ".mkv",
		FileTypeAVI:  ".avi",
		FileTypeMOV:  ".mov",
	}
	for ft, expected := range tests {
		if got := GetExtension(ft); got != expected {
			t.Errorf("GetExtension(%s) = %q, want %q", ft, got, expected)
		}
	}
}

func TestGetMimeType(t *testing.T) {
	tests := map[FileType]string{
		FileTypeJPEG: "image/jpeg",
		FileTypePNG:  "image/png",
		FileTypePDF:  "application/pdf",
		FileTypeMP4:  "video/mp4",
		FileTypeMP3:  "audio/mpeg",
	}
	for ft, expected := range tests {
		if got := GetMimeType(ft); got != expected {
			t.Errorf("GetMimeType(%s) = %q, want %q", ft, got, expected)
		}
	}
}

// OCR Language validation

func TestValidateOCRLanguages(t *testing.T) {
	if !ValidateOCRLanguages([]string{"eng"}) {
		t.Error("expected eng to be valid")
	}
	if !ValidateOCRLanguages([]string{"eng", "fra", "deu"}) {
		t.Error("expected eng+fra+deu to be valid")
	}
	if ValidateOCRLanguages(nil) {
		t.Error("expected nil to be invalid")
	}
	if ValidateOCRLanguages([]string{}) {
		t.Error("expected empty to be invalid")
	}
	if ValidateOCRLanguages([]string{"eng", "fra", "deu", "spa", "ita", "por"}) {
		t.Error("expected >5 languages to be invalid")
	}
	if ValidateOCRLanguages([]string{"xxx"}) {
		t.Error("expected unknown language to be invalid")
	}
}

// Certificate file type detection

func TestDetectCertFileType(t *testing.T) {
	tests := []struct {
		filename string
		expected CertFileType
		valid    bool
	}{
		{"cert.pem", CertTypePEM, true},
		{"cert.crt", CertTypePEM, true},
		{"cert.cer", CertTypePEM, true},
		{"cert.der", CertTypeDER, true},
		{"cert.p12", CertTypeP12, true},
		{"cert.pfx", CertTypeP12, true},
		{"cert.p7b", CertTypeP7B, true},
		{"cert.p7c", CertTypeP7B, true},
		{"cert.txt", "", false},
		{"cert.exe", "", false},
	}
	for _, tc := range tests {
		ct, valid := DetectCertFileType(tc.filename)
		if valid != tc.valid || ct != tc.expected {
			t.Errorf("DetectCertFileType(%q) = (%s, %v), want (%s, %v)", tc.filename, ct, valid, tc.expected, tc.valid)
		}
	}
}

// Filename-based detection

func TestDetectSvgFile(t *testing.T) {
	if !DetectSvgFile("image.svg") {
		t.Error("expected .svg to be detected")
	}
	if !DetectSvgFile("IMAGE.SVG") {
		t.Error("expected .SVG (uppercase) to be detected")
	}
	if DetectSvgFile("image.png") {
		t.Error("expected .png to not be detected as SVG")
	}
}

func TestDetectArchiveFile(t *testing.T) {
	valid := []string{"file.zip", "file.rar", "file.7z", "file.tar.gz", "file.tgz", "file.tar.bz2", "file.tar.xz", "file.tar.zst"}
	for _, f := range valid {
		if !DetectArchiveFile(f) {
			t.Errorf("expected %q to be detected as archive", f)
		}
	}
	if DetectArchiveFile("file.txt") {
		t.Error("expected .txt to not be detected as archive")
	}
}

func TestDetectMarkdownFile(t *testing.T) {
	if !DetectMarkdownFile("README.md") {
		t.Error("expected .md to be detected")
	}
	if !DetectMarkdownFile("doc.markdown") {
		t.Error("expected .markdown to be detected")
	}
	if DetectMarkdownFile("doc.txt") {
		t.Error("expected .txt to not be detected as markdown")
	}
}
