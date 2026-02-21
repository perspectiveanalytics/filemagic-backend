package converter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
	"github.com/perspectiveanalytics/filemagic-backend/internal/queue"
)

type Archiver struct {
	cfg    *config.Config
	runner *NsjailRunner
}

func NewArchiver(cfg *config.Config, runner *NsjailRunner) *Archiver {
	return &Archiver{cfg: cfg, runner: runner}
}

func (a *Archiver) Type() queue.ConversionType {
	return queue.ConversionArchive
}

func (a *Archiver) Convert(ctx context.Context, job *queue.Job) error {
	format, ok := job.Options["format"].(string)
	if !ok || format == "" {
		return fmt.Errorf("missing archive format")
	}

	password, _ := job.Options["password"].(string)

	// Get file list from options (set by handler)
	filesRaw, _ := job.Options["files"].([]any)
	if len(filesRaw) == 0 {
		return fmt.Errorf("no files to archive")
	}

	var fileNames []string
	for _, f := range filesRaw {
		if name, ok := f.(string); ok {
			fileNames = append(fileNames, name)
		}
	}

	jobDir, _, outputDir, err := PrepareJobDir(a.cfg.TmpfsPath, job.ID)
	if err != nil {
		return err
	}

	inputDir := job.InputPath

	ext := archiveExtension(format)
	outputFile := filepath.Join(outputDir, "output"+ext)

	var cmd []string

	switch format {
	case "zip":
		cmd = []string{"/usr/bin/7z", "a", "-tzip"}
		if password != "" {
			cmd = append(cmd, "-mem=AES256", "-p"+password)
		}
		cmd = append(cmd, outputFile)
		for _, name := range fileNames {
			cmd = append(cmd, filepath.Join(inputDir, name))
		}

	case "7z":
		cmd = []string{"/usr/bin/7z", "a", "-t7z"}
		if password != "" {
			cmd = append(cmd, "-mhe=on", "-p"+password)
		}
		cmd = append(cmd, outputFile)
		for _, name := range fileNames {
			cmd = append(cmd, filepath.Join(inputDir, name))
		}

	case "tar.gz":
		cmd = []string{"/usr/bin/tar", "czf", outputFile, "-C", inputDir}
		cmd = append(cmd, fileNames...)

	case "tar.zst":
		cmd = []string{"/usr/bin/tar", "--zstd", "-cf", outputFile, "-C", inputDir}
		cmd = append(cmd, fileNames...)

	default:
		return fmt.Errorf("unsupported archive format: %s", format)
	}

	if err := a.runner.Run(ctx, cmd, jobDir, "archive.cfg", 120*time.Second); err != nil {
		return err
	}

	outputInfo, err := os.Stat(outputFile)
	if err != nil {
		return fmt.Errorf("output file not found: %w", err)
	}

	job.OutputPath = outputFile
	job.OutputSize = outputInfo.Size()
	return nil
}

func archiveExtension(format string) string {
	switch format {
	case "zip":
		return ".zip"
	case "7z":
		return ".7z"
	case "tar.gz":
		return ".tar.gz"
	case "tar.zst":
		return ".tar.zst"
	default:
		return ".zip"
	}
}

// maxFilenameBytes is the maximum length of a sanitized filename in bytes.
// ext4 and most filesystems limit filenames to 255 bytes.
const maxFilenameBytes = 255

// SanitizeFilename returns a safe filename, preserving the original name but
// removing path components and characters that could be problematic when passed
// as arguments to archiving tools (7z, tar).
//
// It defends against:
//   - Path traversal (../../etc/passwd)
//   - Null bytes (truncation attacks)
//   - Shell metacharacters and tool-specific injection
//   - Unicode control characters (zero-width, RTL override, BOM)
//   - Filenames that look like command-line flags (leading -)
//   - tar @-file inclusion (leading @)
//   - Windows reserved device names (CON, NUL, COM1, etc.)
//   - Leading/trailing dots and spaces
//   - Filenames exceeding filesystem limits (255 bytes)
func SanitizeFilename(filename string) string {
	// 1. Strip null bytes BEFORE filepath.Base, since null bytes can cause
	//    C-level string truncation in the underlying OS path routines.
	filename = strings.ReplaceAll(filename, "\x00", "")

	// 2. Strip path components.
	name := filepath.Base(filename)

	// 3. Replace dangerous and problematic characters via strings.Map.
	//    This covers: path separators, quotes, shell metacharacters, brackets,
	//    redirections, all C0 control characters (0x00-0x1F), DEL (0x7F),
	//    and Unicode control/format characters used in attacks.
	name = strings.Map(func(r rune) rune {
		// C0 control characters and DEL
		if r <= 0x1F || r == 0x7F {
			return '_'
		}
		switch r {
		case '/', '\\', // path separators
			'\'', '"', '`', // quotes
			'$', '!', ';', '&', '|', // shell metacharacters
			'(', ')', '{', '}', '[', ']', // brackets
			'<', '>', // redirections
			'@',  // tar @-file inclusion
			'#',  // comment character in some tools
			'~',  // home directory expansion
			'*',  // glob wildcard
			'?',  // glob wildcard
			'\u200B', // zero-width space
			'\u200C', // zero-width non-joiner
			'\u200D', // zero-width joiner
			'\u200E', // left-to-right mark
			'\u200F', // right-to-left mark
			'\u202A', // left-to-right embedding
			'\u202B', // right-to-left embedding
			'\u202C', // pop directional formatting
			'\u202D', // left-to-right override
			'\u202E', // right-to-left override
			'\u2060', // word joiner
			'\u2061', // function application
			'\u2062', // invisible times
			'\u2063', // invisible separator
			'\u2064', // invisible plus
			'\uFEFF', // BOM / zero-width no-break space
			'\uFFF9', // interlinear annotation anchor
			'\uFFFA', // interlinear annotation separator
			'\uFFFB': // interlinear annotation terminator
			return '_'
		}
		return r
	}, name)

	// 4. Fallback for empty, dot, or double-dot results.
	if name == "" || name == "." || name == ".." {
		return "file"
	}

	// 5. Strip leading/trailing dots and spaces.
	//    Leading dots create hidden files on Unix; trailing dots/spaces
	//    cause issues on Windows filesystems.
	name = strings.Trim(name, ". ")
	if name == "" {
		return "file"
	}

	// 6. Prevent filenames starting with '-' (interpreted as flags by 7z, tar, etc.)
	if name[0] == '-' {
		name = "_" + name[1:]
	}

	// 7. Reject Windows reserved device names.
	//    These cause problems on NTFS/CIFS and when files are served to Windows clients.
	upper := strings.ToUpper(name)
	base := upper
	if dot := strings.IndexByte(base, '.'); dot >= 0 {
		base = base[:dot]
	}
	switch base {
	case "CON", "PRN", "AUX", "NUL",
		"COM0", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT0", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		name = "_" + name
	}

	// 8. Truncate to maxFilenameBytes while respecting UTF-8 boundaries.
	if len(name) > maxFilenameBytes {
		name = name[:maxFilenameBytes]
		// Walk backwards past any incomplete UTF-8 sequence at the end.
		for len(name) > 0 && !utf8.Valid([]byte(name)) {
			name = name[:len(name)-1]
		}
		if len(name) == 0 {
			return "file"
		}
	}

	return name
}
