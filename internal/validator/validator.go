package validator

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
)

type FileType string

const (
	FileTypeJPEG FileType = "jpeg"
	FileTypePNG  FileType = "png"
	FileTypeWEBP FileType = "webp"
	FileTypeHEIC FileType = "heic"
	FileTypeBMP  FileType = "bmp"
	FileTypeTIFF FileType = "tiff"
	FileTypePDF  FileType = "pdf"

	// Video types
	FileTypeMP4 FileType = "mp4"
	FileTypeMOV FileType = "mov"
	FileTypeMKV FileType = "mkv" // Also covers WebM (same EBML container)
	FileTypeAVI FileType = "avi"

	// Audio types
	FileTypeMP3  FileType = "mp3"
	FileTypeWAV  FileType = "wav"
	FileTypeFLAC FileType = "flac"
	FileTypeM4A  FileType = "m4a"
)

var (
	ErrUnknownFileType = errors.New("unknown file type")
	ErrFileTooLarge    = errors.New("file too large")
	ErrInvalidFileType = errors.New("invalid file type for this conversion")
)

var magicBytes = map[FileType][]magicPattern{
	FileTypeJPEG: {
		{[]byte{0xFF, 0xD8, 0xFF}, 0},
	},
	FileTypePNG: {
		{[]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, 0},
	},
	FileTypeWEBP: {
		{[]byte("RIFF"), 0}, // secondary check for "WEBP" at offset 8
	},
	FileTypeHEIC: {
		{[]byte("ftypheic"), 4},
		{[]byte("ftypmif1"), 4},
		{[]byte("ftypmsf1"), 4},
		{[]byte("ftypheix"), 4},
		{[]byte("ftyphevc"), 4},
		{[]byte("ftypheim"), 4},
		{[]byte("ftypheis"), 4},
		{[]byte("ftyphevm"), 4},
		{[]byte("ftyphevs"), 4},
	},
	FileTypeBMP: {
		{[]byte{0x42, 0x4D}, 0},
	},
	FileTypeTIFF: {
		{[]byte{0x49, 0x49, 0x2A, 0x00}, 0}, // little-endian
		{[]byte{0x4D, 0x4D, 0x00, 0x2A}, 0}, // big-endian
	},
	FileTypePDF: {
		{[]byte("%PDF"), 0},
	},

	// Audio types
	FileTypeMP3: {
		{[]byte("ID3"), 0},           // ID3v2 tag
		{[]byte{0xFF, 0xFB}, 0},      // MPEG Layer 3 frame sync
		{[]byte{0xFF, 0xF3}, 0},      // MPEG Layer 3 frame sync
		{[]byte{0xFF, 0xF2}, 0},      // MPEG Layer 3 frame sync
	},
	FileTypeFLAC: {
		{[]byte("fLaC"), 0},
	},
	FileTypeWAV: {
		{[]byte("RIFF"), 0}, // secondary check for "WAVE" at offset 8
	},

	// Video types — ftyp-based (offset 4)
	FileTypeMP4: {
		{[]byte("ftypisom"), 4},
		{[]byte("ftypiso2"), 4},
		{[]byte("ftypmp41"), 4},
		{[]byte("ftypmp42"), 4},
		{[]byte("ftypavc1"), 4},
		{[]byte("ftypdash"), 4},
		{[]byte("ftypMSNV"), 4},
	},
	FileTypeMOV: {
		{[]byte("ftypqt  "), 4},
	},
	FileTypeM4A: {
		{[]byte("ftypM4A "), 4},
		{[]byte("ftypM4B "), 4},
	},
	FileTypeMKV: {
		{[]byte{0x1A, 0x45, 0xDF, 0xA3}, 0}, // EBML header (MKV and WebM)
	},
	FileTypeAVI: {
		{[]byte("RIFF"), 0}, // secondary check for "AVI " at offset 8
	},
}

type magicPattern struct {
	pattern []byte
	offset  int
}

func DetectFileType(r io.Reader) (FileType, error) {
	header := make([]byte, 32)
	n, err := r.Read(header)
	if err != nil && err != io.EOF {
		return "", err
	}
	header = header[:n]

	for fileType, patterns := range magicBytes {
		for _, p := range patterns {
			if p.offset+len(p.pattern) <= len(header) {
				if bytes.Equal(header[p.offset:p.offset+len(p.pattern)], p.pattern) {
					// RIFF container disambiguation
					if (fileType == FileTypeWEBP || fileType == FileTypeWAV || fileType == FileTypeAVI) && len(header) >= 12 {
						subtype := string(header[8:12])
						switch subtype {
						case "WEBP":
							if fileType != FileTypeWEBP {
								continue
							}
						case "WAVE":
							if fileType != FileTypeWAV {
								continue
							}
						case "AVI ":
							if fileType != FileTypeAVI {
								continue
							}
						default:
							continue
						}
					}

					// Structural validation to reduce false positives
					if !validateStructure(fileType, header) {
						continue
					}

					return fileType, nil
				}
			}
		}
	}

	return "", ErrUnknownFileType
}

// validateStructure performs deeper checks beyond magic bytes to reduce
// false positives from short signatures (MP3 2-byte, BMP 2-byte, etc.).
func validateStructure(ft FileType, header []byte) bool {
	switch ft {
	case FileTypeJPEG:
		// After SOI (FF D8), byte 2 must be FF and byte 3 a valid marker.
		// Valid markers after SOI: APP0-APP15 (E0-EF), DQT (DB), SOF (C0-CF), COM (FE), DHP (DE).
		if len(header) < 4 {
			return false
		}
		m := header[3]
		return (m >= 0xC0 && m <= 0xCF) || (m >= 0xDB && m <= 0xFE)

	case FileTypePNG:
		// After 8-byte signature, bytes 12-15 must be "IHDR".
		if len(header) < 16 {
			return false
		}
		return bytes.Equal(header[12:16], []byte("IHDR"))

	case FileTypeBMP:
		// Bytes 6-9 (reserved fields) must be zero in valid BMPs.
		if len(header) < 10 {
			return false
		}
		return header[6] == 0 && header[7] == 0 && header[8] == 0 && header[9] == 0

	case FileTypeMP3:
		// ID3 tag header (3 bytes) is sufficient — it's a structured header.
		if len(header) >= 3 && bytes.Equal(header[0:3], []byte("ID3")) {
			return true
		}
		// For MPEG sync (FF Fx), validate the full 4-byte frame header:
		//   bits 11-12: MPEG version (01 is reserved → reject)
		//   bits 13-14: layer (00 is reserved → reject)
		//   bits 16-19: bitrate index (1111 is invalid)
		//   bits 20-21: sample rate index (11 is reserved)
		if len(header) < 4 {
			return false
		}
		b1 := header[1]
		b2 := header[2]
		mpegVersion := (b1 >> 3) & 0x03 // bits 11-12
		layer := (b1 >> 1) & 0x03       // bits 13-14
		bitrateIdx := (b2 >> 4) & 0x0F  // bits 16-19
		sampleIdx := (b2 >> 2) & 0x03   // bits 20-21
		if mpegVersion == 0x01 {         // reserved
			return false
		}
		if layer == 0x00 { // reserved
			return false
		}
		if bitrateIdx == 0x0F { // invalid
			return false
		}
		if sampleIdx == 0x03 { // reserved
			return false
		}
		return true

	case FileTypeMP4, FileTypeMOV, FileTypeM4A, FileTypeHEIC:
		// ISO BMFF: bytes 0-3 are the ftyp box size (big-endian uint32).
		// Must be >= 8 (minimum box: 4 size + 4 type) and <= 1024 (ftyp boxes are small).
		if len(header) < 12 {
			return false
		}
		boxSize := binary.BigEndian.Uint32(header[0:4])
		return boxSize >= 8 && boxSize <= 1024

	case FileTypeWAV, FileTypeWEBP, FileTypeAVI:
		// RIFF chunk size (bytes 4-7, little-endian) must be > 0 and not 0xFFFFFFFF.
		if len(header) < 12 {
			return false
		}
		chunkSize := binary.LittleEndian.Uint32(header[4:8])
		return chunkSize > 0 && chunkSize != 0xFFFFFFFF

	default:
		return true
	}
}

func ValidateForImageConversion(fileType FileType) bool {
	switch fileType {
	case FileTypeJPEG, FileTypePNG, FileTypeWEBP, FileTypeHEIC, FileTypeBMP, FileTypeTIFF:
		return true
	default:
		return false
	}
}

func ValidateForPDFCompression(fileType FileType) bool {
	return fileType == FileTypePDF
}

func ValidateForImageCompression(fileType FileType) bool {
	switch fileType {
	case FileTypeJPEG, FileTypePNG:
		return true
	default:
		return false
	}
}

func ValidateForMetadataRemoval(fileType FileType) bool {
	switch fileType {
	case FileTypeJPEG, FileTypePNG, FileTypeWEBP, FileTypeHEIC, FileTypeTIFF, FileTypePDF:
		return true
	default:
		return false
	}
}

func ValidateForPDFMerge(fileType FileType) bool {
	switch fileType {
	case FileTypePDF, FileTypeJPEG, FileTypePNG, FileTypeWEBP, FileTypeBMP, FileTypeTIFF:
		return true
	default:
		return false
	}
}

func ValidateForImageToPDF(fileType FileType) bool {
	switch fileType {
	case FileTypeJPEG, FileTypePNG, FileTypeWEBP, FileTypeBMP, FileTypeTIFF:
		return true
	default:
		return false
	}
}

func ValidateForOCR(fileType FileType) bool {
	switch fileType {
	case FileTypeJPEG, FileTypePNG, FileTypeWEBP, FileTypeHEIC, FileTypeBMP, FileTypeTIFF, FileTypePDF:
		return true
	default:
		return false
	}
}

func ValidateForAudioExtract(fileType FileType) bool {
	switch fileType {
	case FileTypeMP4, FileTypeMOV, FileTypeMKV, FileTypeAVI:
		return true
	default:
		return false
	}
}

func ValidateForAudioConvert(fileType FileType) bool {
	switch fileType {
	case FileTypeMP3, FileTypeWAV, FileTypeFLAC, FileTypeM4A:
		return true
	default:
		return false
	}
}

func ValidateForVideoCompress(fileType FileType) bool {
	switch fileType {
	case FileTypeMP4, FileTypeMOV, FileTypeMKV, FileTypeAVI:
		return true
	default:
		return false
	}
}

func ValidateForMovToMp4(fileType FileType) bool {
	return fileType == FileTypeMOV
}

func ValidateForPDFPassword(fileType FileType) bool {
	return fileType == FileTypePDF
}

func ValidateForPDFExtractImages(fileType FileType) bool {
	return fileType == FileTypePDF
}

// DetectFontFile checks if a filename has a supported font extension.
func DetectFontFile(filename string) bool {
	lower := strings.ToLower(filename)
	for _, ext := range []string{".ttf", ".otf", ".woff", ".woff2"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// DetectEbookFile checks if a filename has a supported ebook extension.
func DetectEbookFile(filename string) bool {
	lower := strings.ToLower(filename)
	for _, ext := range []string{
		".epub", ".mobi", ".azw3", ".azw", ".pdf", ".fb2",
		".txt", ".docx", ".html", ".htm", ".rtf", ".odt",
		".lit", ".pdb", ".cbz", ".cbr",
	} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// DetectSvgFile checks if a filename has an SVG extension.
func DetectSvgFile(filename string) bool {
	return strings.HasSuffix(strings.ToLower(filename), ".svg")
}

// DetectArchiveFile checks if a filename has a supported archive extension.
func DetectArchiveFile(filename string) bool {
	lower := strings.ToLower(filename)
	for _, ext := range []string{".zip", ".rar", ".7z", ".tar.gz", ".tgz", ".tar.bz2", ".tar.xz", ".tar.zst"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

// DetectMarkdownFile checks if a filename has a markdown extension.
func DetectMarkdownFile(filename string) bool {
	lower := strings.ToLower(filename)
	return strings.HasSuffix(lower, ".md") || strings.HasSuffix(lower, ".markdown")
}

var ValidOCRLanguages = map[string]bool{
	"eng":     true,
	"fra":     true,
	"deu":     true,
	"spa":     true,
	"ita":     true,
	"por":     true,
	"nld":     true,
	"rus":     true,
	"ara":     true,
	"chi_sim": true,
	"chi_tra": true,
	"jpn":     true,
	"kor":     true,
}

func ValidateOCRLanguages(languages []string) bool {
	if len(languages) == 0 || len(languages) > 5 {
		return false
	}
	for _, lang := range languages {
		if !ValidOCRLanguages[lang] {
			return false
		}
	}
	return true
}

// CertFileType represents certificate file formats detected by extension.
type CertFileType string

const (
	CertTypePEM CertFileType = "pem"
	CertTypeDER CertFileType = "der"
	CertTypeP12 CertFileType = "p12"
	CertTypeP7B CertFileType = "p7b"
)

// DetectCertFileType detects certificate format from filename extension.
func DetectCertFileType(filename string) (CertFileType, bool) {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".pem"),
		strings.HasSuffix(lower, ".crt"),
		strings.HasSuffix(lower, ".cer"),
		strings.HasSuffix(lower, ".csr"):
		return CertTypePEM, true
	case strings.HasSuffix(lower, ".der"):
		return CertTypeDER, true
	case strings.HasSuffix(lower, ".p12"),
		strings.HasSuffix(lower, ".pfx"):
		return CertTypeP12, true
	case strings.HasSuffix(lower, ".p7b"),
		strings.HasSuffix(lower, ".p7c"):
		return CertTypeP7B, true
	default:
		return "", false
	}
}

// GetCertExtension returns the file extension for a cert type.
func GetCertExtension(ct CertFileType) string {
	switch ct {
	case CertTypePEM:
		return ".pem"
	case CertTypeDER:
		return ".der"
	case CertTypeP12:
		return ".p12"
	case CertTypeP7B:
		return ".p7b"
	default:
		return ""
	}
}

func GetMimeType(ft FileType) string {
	switch ft {
	case FileTypeJPEG:
		return "image/jpeg"
	case FileTypePNG:
		return "image/png"
	case FileTypeWEBP:
		return "image/webp"
	case FileTypeHEIC:
		return "image/heic"
	case FileTypeBMP:
		return "image/bmp"
	case FileTypeTIFF:
		return "image/tiff"
	case FileTypePDF:
		return "application/pdf"
	case FileTypeMP4:
		return "video/mp4"
	case FileTypeMOV:
		return "video/quicktime"
	case FileTypeMKV:
		return "video/x-matroska"
	case FileTypeAVI:
		return "video/x-msvideo"
	case FileTypeMP3:
		return "audio/mpeg"
	case FileTypeWAV:
		return "audio/wav"
	case FileTypeFLAC:
		return "audio/flac"
	case FileTypeM4A:
		return "audio/mp4"
	default:
		return "application/octet-stream"
	}
}

func GetExtension(ft FileType) string {
	switch ft {
	case FileTypeJPEG:
		return ".jpg"
	case FileTypePNG:
		return ".png"
	case FileTypeWEBP:
		return ".webp"
	case FileTypeHEIC:
		return ".heic"
	case FileTypeBMP:
		return ".bmp"
	case FileTypeTIFF:
		return ".tiff"
	case FileTypePDF:
		return ".pdf"
	case FileTypeMP4:
		return ".mp4"
	case FileTypeMOV:
		return ".mov"
	case FileTypeMKV:
		return ".mkv"
	case FileTypeAVI:
		return ".avi"
	case FileTypeMP3:
		return ".mp3"
	case FileTypeWAV:
		return ".wav"
	case FileTypeFLAC:
		return ".flac"
	case FileTypeM4A:
		return ".m4a"
	default:
		return ""
	}
}
