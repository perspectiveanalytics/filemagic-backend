package converter

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		want     string
		checkFn  func(t *testing.T, got string) // optional extra validation
	}{
		// 1. Normal / passthrough cases
		{
			name:  "simple ASCII filename",
			input: "report.pdf",
			want:  "report.pdf",
		},
		{
			name:  "filename with spaces",
			input: "my report.pdf",
			want:  "my report.pdf",
		},
		{
			name:  "filename with hyphens and underscores",
			input: "my-report_2024.pdf",
			want:  "my-report_2024.pdf",
		},
		{
			name:  "filename with numbers",
			input: "12345.txt",
			want:  "12345.txt",
		},
		{
			name:  "double extension preserved",
			input: "archive.tar.gz",
			want:  "archive.tar.gz",
		},
		{
			name:  "triple extension preserved",
			input: "backup.2024.tar.gz",
			want:  "backup.2024.tar.gz",
		},

		// 2. Path traversal attacks
		{
			name:  "path traversal with ../",
			input: "../../etc/passwd",
			want:  "passwd",
		},
		{
			name:  "path traversal with ..\\ (Linux: backslash is literal)",
			input: `..\..\etc\passwd`,
			want:  "_.._etc_passwd", // On Linux, \ is not a path separator; filepath.Base keeps the whole string, then \ -> _
		},
		{
			name:  "absolute path Unix",
			input: "/etc/passwd",
			want:  "passwd",
		},
		{
			name:  "absolute path Windows (Linux: backslash is literal)",
			input: `C:\Windows\System32\cmd.exe`,
			want:  "C:_Windows_System32_cmd.exe", // On Linux, \ is not a path separator
		},
		{
			name:  "deep path traversal",
			input: "../../../../../../../../etc/shadow",
			want:  "shadow",
		},
		{
			name:  "path traversal with mixed separators",
			input: `../..\/etc/passwd`,
			want:  "passwd",
		},
		{
			name:  "just double dots",
			input: "..",
			want:  "file",
		},
		{
			name:  "just a single dot",
			input: ".",
			want:  "file",
		},
		{
			name:  "path with trailing slash",
			input: "somedir/",
			want:  "somedir", // filepath.Base("somedir/") returns "somedir"
		},
		{
			name:  "relative path without traversal",
			input: "subdir/report.pdf",
			want:  "report.pdf",
		},

		// 3. Null byte attacks
		{
			name:  "null byte in middle",
			input: "evil\x00.txt",
			want:  "evil.txt",
		},
		{
			name:  "null byte before extension",
			input: "image.php\x00.jpg",
			want:  "image.php.jpg",
		},
		{
			name:  "null byte before path traversal",
			input: "safe.txt\x00../../etc/passwd",
			want:  "passwd",
		},
		{
			name:  "only null bytes",
			input: "\x00\x00\x00",
			want:  "file",
		},
		{
			name:  "null byte at start",
			input: "\x00malicious.sh",
			want:  "malicious.sh",
		},
		{
			name:  "null bytes in path components",
			input: "dir\x00name/file\x00name.txt",
			want:  "filename.txt",
		},

		// 4. Unicode normalization / control character attacks
		{
			name:  "zero-width space in filename",
			input: "test\u200Bfile.txt",
			want:  "test_file.txt",
		},
		{
			name:  "zero-width joiner",
			input: "test\u200Dfile.txt",
			want:  "test_file.txt",
		},
		{
			name:  "zero-width non-joiner",
			input: "test\u200Cfile.txt",
			want:  "test_file.txt",
		},
		{
			name:  "RTL override to disguise extension",
			input: "invoice\u202Efdp.exe",
			want:  "invoice_fdp.exe",
		},
		{
			name:  "left-to-right override",
			input: "test\u202Dfile.txt",
			want:  "test_file.txt",
		},
		{
			name:  "BOM in filename",
			input: "\uFEFFtest.txt",
			want:  "_test.txt", // BOM replaced with _, underscore is preserved
		},
		{
			name:  "multiple zero-width chars",
			input: "\u200B\u200C\u200D\u200E\u200F.txt",
			want:  "_____.txt", // ZW chars replaced with _, underscores preserved (only dots/spaces trimmed)
		},
		{
			name:  "word joiner",
			input: "test\u2060file.txt",
			want:  "test_file.txt",
		},
		{
			name:  "interlinear annotation",
			input: "test\uFFF9anno\uFFFA\uFFFBfile.txt",
			want:  "test_anno__file.txt",
		},
		{
			name:  "left-to-right mark",
			input: "file\u200E.txt",
			want:  "file_.txt",
		},
		{
			name:  "right-to-left mark",
			input: "file\u200F.txt",
			want:  "file_.txt",
		},

		// 5. Non-ASCII characters (should be preserved)
		{
			name:  "Cyrillic filename",
			input: "документ.pdf",
			want:  "документ.pdf",
		},
		{
			name:  "Chinese filename",
			input: "报告.docx",
			want:  "报告.docx",
		},
		{
			name:  "Japanese filename",
			input: "テスト.txt",
			want:  "テスト.txt",
		},
		{
			name:  "Arabic filename",
			input: "تقرير.pdf",
			want:  "تقرير.pdf",
		},
		{
			name:  "emoji in filename",
			input: "fun😀file.txt",
			want:  "fun😀file.txt",
		},
		{
			name:  "accented characters",
			input: "résumé.pdf",
			want:  "résumé.pdf",
		},
		{
			name:  "German umlauts",
			input: "Ärger_über_Öffnung.txt",
			want:  "Ärger_über_Öffnung.txt",
		},

		// 6. Shell metacharacter injection
		{
			name:  "semicolon injection (contains / so filepath.Base extracts last component)",
			input: "file;rm -rf /.txt",
			want:  "txt", // filepath.Base returns ".txt", then leading dot stripped
		},
		{
			name:  "pipe injection (contains / so filepath.Base extracts last component)",
			input: "file|cat /etc/passwd.txt",
			want:  "passwd.txt", // filepath.Base returns "passwd.txt"
		},
		{
			name:  "ampersand injection",
			input: "file&wget evil.com.txt",
			want:  "file_wget evil.com.txt",
		},
		{
			name:  "dollar sign expansion",
			input: "file$(whoami).txt",
			want:  "file__whoami_.txt",
		},
		{
			name:  "backtick injection",
			input: "file`id`.txt",
			want:  "file_id_.txt",
		},
		{
			name:  "single quotes",
			input: "file'name'.txt",
			want:  "file_name_.txt",
		},
		{
			name:  "double quotes",
			input: `file"name".txt`,
			want:  "file_name_.txt",
		},
		{
			name:  "exclamation mark (history expansion)",
			input: "file!important.txt",
			want:  "file_important.txt",
		},
		{
			name:  "angle brackets (redirect)",
			input: "file>output.txt",
			want:  "file_output.txt",
		},
		{
			name:  "curly braces",
			input: "file{a,b}.txt",
			want:  "file_a,b_.txt",
		},
		{
			name:  "square brackets",
			input: "file[0].txt",
			want:  "file_0_.txt",
		},
		{
			name:  "parentheses",
			input: "file(1).txt",
			want:  "file_1_.txt",
		},
		{
			name:  "hash character",
			input: "#comment.txt",
			want:  "_comment.txt", // # replaced with _, underscore is preserved
		},
		{
			name:  "tilde expansion",
			input: "~user/.bashrc",
			want:  "bashrc", // filepath.Base extracts .bashrc, then leading dot stripped
		},
		{
			name:  "glob wildcards star",
			input: "*.txt",
			want:  "_.txt", // * replaced with _, underscore preserved (only dots/spaces trimmed)
		},
		{
			name:  "glob wildcards question mark",
			input: "file?.txt",
			want:  "file_.txt",
		},

		// 7. Tool-specific injection (7z, tar)
		{
			name:  "leading dash (flag injection for tar/7z)",
			input: "-rf",
			want:  "_rf",
		},
		{
			name:  "leading double dash",
			input: "--checkpoint-action=exec=sh",
			want:  "_-checkpoint-action=exec=sh", // leading - replaced with _, = is not in blocked list
		},
		{
			name:  "tar @-file inclusion",
			input: "@filelist.txt",
			want:  "_filelist.txt",
		},
		{
			name:  "leading dash with extension",
			input: "-e .txt",
			want:  "_e .txt", // leading dot would be stripped but there's none
		},
		{
			name:  "7z switch injection",
			input: "-si",
			want:  "_si",
		},

		// 8. Windows reserved device names
		{
			name:  "CON",
			input: "CON",
			want:  "_CON",
		},
		{
			name:  "con lowercase",
			input: "con",
			want:  "_con",
		},
		{
			name:  "CON with extension",
			input: "CON.txt",
			want:  "_CON.txt",
		},
		{
			name:  "PRN",
			input: "PRN",
			want:  "_PRN",
		},
		{
			name:  "AUX",
			input: "AUX",
			want:  "_AUX",
		},
		{
			name:  "NUL device",
			input: "NUL",
			want:  "_NUL",
		},
		{
			name:  "NUL with extension",
			input: "NUL.txt",
			want:  "_NUL.txt",
		},
		{
			name:  "COM1",
			input: "COM1",
			want:  "_COM1",
		},
		{
			name:  "COM9",
			input: "COM9",
			want:  "_COM9",
		},
		{
			name:  "LPT1",
			input: "LPT1",
			want:  "_LPT1",
		},
		{
			name:  "LPT9",
			input: "LPT9",
			want:  "_LPT9",
		},
		{
			name:  "COM0",
			input: "COM0",
			want:  "_COM0",
		},
		{
			name:  "LPT0",
			input: "LPT0",
			want:  "_LPT0",
		},
		{
			name:  "mixed case NuL",
			input: "NuL",
			want:  "_NuL",
		},
		{
			name:  "mixed case Aux.txt",
			input: "Aux.txt",
			want:  "_Aux.txt",
		},
		{
			name:  "COM1 with long extension",
			input: "COM1.tar.gz",
			want:  "_COM1.tar.gz",
		},
		{
			name:  "not a reserved name (CONX)",
			input: "CONX.txt",
			want:  "CONX.txt",
		},
		{
			name:  "not a reserved name (COM10)",
			input: "COM10.txt",
			want:  "COM10.txt",
		},

		// 9. Leading/trailing dots and spaces
		{
			name:  "leading dot (hidden file on Unix)",
			input: ".hidden",
			want:  "hidden",
		},
		{
			name:  "trailing dot",
			input: "file.",
			want:  "file",
		},
		{
			name:  "multiple leading dots",
			input: "...file.txt",
			want:  "file.txt",
		},
		{
			name:  "multiple trailing dots",
			input: "file...",
			want:  "file",
		},
		{
			name:  "leading spaces",
			input: "   file.txt",
			want:  "file.txt",
		},
		{
			name:  "trailing spaces",
			input: "file.txt   ",
			want:  "file.txt",
		},
		{
			name:  "leading and trailing spaces and dots",
			input: " . .file.txt. . ",
			want:  "file.txt",
		},
		{
			name:  "only dots",
			input: "...",
			want:  "file",
		},
		{
			name:  "only spaces",
			input: "     ",
			want:  "file",
		},
		{
			name:  "dots and spaces only",
			input: " . . . ",
			want:  "file",
		},

		// 10. Empty and minimal inputs
		{
			name:  "empty string",
			input: "",
			want:  "file",
		},
		{
			name:  "single space",
			input: " ",
			want:  "file",
		},
		{
			name:  "single dot",
			input: ".",
			want:  "file",
		},
		{
			name:  "double dot",
			input: "..",
			want:  "file",
		},

		// 11. Long filenames (DoS via filesystem limits)
		{
			name:  "exactly 255 bytes",
			input: strings.Repeat("a", 255),
			want:  strings.Repeat("a", 255),
		},
		{
			name:  "256 bytes truncated to 255",
			input: strings.Repeat("a", 256),
			want:  strings.Repeat("a", 255),
		},
		{
			name:  "1000 bytes truncated to 255",
			input: strings.Repeat("b", 1000),
			want:  strings.Repeat("b", 255),
		},
		{
			name:  "long filename with extension truncated",
			input: strings.Repeat("c", 300) + ".txt",
			want:  strings.Repeat("c", 255),
			checkFn: func(t *testing.T, got string) {
				if len(got) > maxFilenameBytes {
					t.Errorf("result exceeds %d bytes: got %d", maxFilenameBytes, len(got))
				}
			},
		},
		{
			name:  "long multibyte filename truncated on rune boundary",
			input: strings.Repeat("Д", 200), // Д is 2 bytes in UTF-8, 200 runes = 400 bytes
			checkFn: func(t *testing.T, got string) {
				if len(got) > maxFilenameBytes {
					t.Errorf("result exceeds %d bytes: got %d", maxFilenameBytes, len(got))
				}
				if !utf8.ValidString(got) {
					t.Error("result is not valid UTF-8")
				}
				// 255 / 2 = 127 runes (254 bytes), since 128 runes = 256 bytes > 255
				if utf8.RuneCountInString(got) != 127 {
					t.Errorf("expected 127 runes, got %d", utf8.RuneCountInString(got))
				}
			},
		},
		{
			name:  "long 4-byte emoji filename truncated on rune boundary",
			input: strings.Repeat("😀", 100), // 4 bytes each, 400 bytes total
			checkFn: func(t *testing.T, got string) {
				if len(got) > maxFilenameBytes {
					t.Errorf("result exceeds %d bytes: got %d", maxFilenameBytes, len(got))
				}
				if !utf8.ValidString(got) {
					t.Error("result is not valid UTF-8")
				}
				// 255 / 4 = 63 runes (252 bytes)
				if utf8.RuneCountInString(got) != 63 {
					t.Errorf("expected 63 runes, got %d", utf8.RuneCountInString(got))
				}
			},
		},
		{
			name:  "long 3-byte CJK filename truncated on rune boundary",
			input: strings.Repeat("中", 100), // 3 bytes each, 300 bytes total
			checkFn: func(t *testing.T, got string) {
				if len(got) > maxFilenameBytes {
					t.Errorf("result exceeds %d bytes: got %d", maxFilenameBytes, len(got))
				}
				if !utf8.ValidString(got) {
					t.Error("result is not valid UTF-8")
				}
				// 255 / 3 = 85 runes (255 bytes)
				if utf8.RuneCountInString(got) != 85 {
					t.Errorf("expected 85 runes, got %d", utf8.RuneCountInString(got))
				}
			},
		},

		// 12. Control characters (beyond \n, \r, \t)
		{
			name:  "bell character",
			input: "file\x07name.txt",
			want:  "file_name.txt",
		},
		{
			name:  "backspace character",
			input: "file\x08name.txt",
			want:  "file_name.txt",
		},
		{
			name:  "escape character",
			input: "file\x1bname.txt",
			want:  "file_name.txt",
		},
		{
			name:  "form feed",
			input: "file\x0cname.txt",
			want:  "file_name.txt",
		},
		{
			name:  "vertical tab",
			input: "file\x0bname.txt",
			want:  "file_name.txt",
		},
		{
			name:  "DEL character (0x7F)",
			input: "file\x7fname.txt",
			want:  "file_name.txt",
		},
		{
			name:  "SOH character (0x01)",
			input: "file\x01name.txt",
			want:  "file_name.txt",
		},
		{
			name:  "newline in filename",
			input: "file\nname.txt",
			want:  "file_name.txt",
		},
		{
			name:  "carriage return in filename",
			input: "file\rname.txt",
			want:  "file_name.txt",
		},
		{
			name:  "tab in filename",
			input: "file\tname.txt",
			want:  "file_name.txt",
		},

		// 13. Combined/compound attacks
		{
			name:  "path traversal + null byte",
			input: "../../\x00etc/passwd",
			want:  "passwd",
		},
		{
			name:  "null byte + shell injection (contains / so filepath.Base extracts last component)",
			input: "test\x00;rm -rf /.txt",
			want:  "txt", // null bytes removed -> "test;rm -rf /.txt", filepath.Base -> ".txt", dot stripped -> "txt"
		},
		{
			name:  "RTL override to disguise .exe as .pdf",
			input: "document\u202Efdp.exe",
			want:  "document_fdp.exe",
		},
		{
			name:  "path traversal + reserved name",
			input: "../../CON.txt",
			want:  "_CON.txt",
		},
		{
			name:  "leading dash + path traversal",
			input: "../-rf",
			want:  "_rf",
		},
		{
			name:  "zero-width chars + path traversal",
			input: "../\u200B\u200Ctest.txt",
			want:  "__test.txt", // filepath.Base -> "\u200B\u200Ctest.txt", ZW chars -> "__test.txt"
		},
		{
			name:  "all problematic chars combined",
			input: `../te;st$"file|name&.txt`,
			want:  "te_st__file_name_.txt",
		},

		// 14. Edge cases with the @ character (tar specific)
		{
			name:  "@ at start",
			input: "@filelist",
			want:  "_filelist",
		},
		{
			name:  "@ in middle",
			input: "user@host.txt",
			want:  "user_host.txt",
		},
		{
			name:  "@ with path",
			input: "dir/@list.txt",
			want:  "_list.txt", // @ replaced with _, then leading _ stays. But dot before list? No: @list.txt -> _list.txt
		},

		// 15. Percent encoding / URL-like filenames (should be preserved as-is)
		{
			name:  "percent encoded chars preserved",
			input: "file%20name.txt",
			want:  "file%20name.txt",
		},
		{
			name:  "plus sign preserved",
			input: "file+name.txt",
			want:  "file+name.txt",
		},
		{
			name:  "equals sign preserved",
			input: "key=value.txt",
			want:  "key=value.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeFilename(tt.input)

			// Check exact match if want is specified
			if tt.want != "" && got != tt.want {
				t.Errorf("SanitizeFilename(%q) = %q, want %q", tt.input, got, tt.want)
			}

			// Run custom validation if provided
			if tt.checkFn != nil {
				tt.checkFn(t, got)
			}
		})
	}
}

// TestSanitizeFilenameInvariants checks properties that must hold for ANY input.
func TestSanitizeFilenameInvariants(t *testing.T) {
	// A collection of adversarial and random inputs to fuzz invariants.
	inputs := []string{
		"",
		".",
		"..",
		"...",
		"/",
		"\\",
		"\x00",
		"\x00\x00\x00",
		" ",
		"   ",
		"-",
		"--",
		"-rf",
		"@",
		"@@",
		"@filelist",
		"CON",
		"con",
		"PRN",
		"NUL",
		"COM1",
		"LPT1",
		"CON.txt",
		"NUL.tar.gz",
		strings.Repeat("x", 1000),
		strings.Repeat("\x00", 100),
		strings.Repeat(".", 100),
		strings.Repeat(" ", 100),
		strings.Repeat("-", 100),
		strings.Repeat("@", 100),
		"../../etc/passwd",
		"../../../../../../../etc/shadow",
		"/etc/passwd",
		`C:\Windows\System32\cmd.exe`,
		"file\x00name",
		"file\nname",
		"file\rname",
		"file\tname",
		"\u200Btest",
		"\u200Ctest",
		"\u200Dtest",
		"\u202Etest",
		"\uFEFFtest",
		"test\u200B\u200C\u200D\u200E\u200F\u202A\u202B\u202C\u202D\u202E",
		";rm -rf /",
		"$(whoami)",
		"`id`",
		"file|cat /etc/passwd",
		"file&wget evil.com",
		"file>output",
		"file<input",
		"'quoted'",
		`"doublequoted"`,
		"hello world.txt",
		"résumé.pdf",
		"документ.pdf",
		"テスト.txt",
		strings.Repeat("Д", 200),
		strings.Repeat("😀", 100),
		strings.Repeat("中", 100),
		".hidden",
		"..hidden",
		"file.",
		"file...",
		" file.txt ",
		"  ..file.txt..  ",
		"-e cmd",
		"--checkpoint=1",
		"--checkpoint-action=exec=sh",
	}

	for _, input := range inputs {
		t.Run("invariants/"+sanitizeTestName(input), func(t *testing.T) {
			got := SanitizeFilename(input)

			// Invariant 1: result is never empty
			if got == "" {
				t.Errorf("SanitizeFilename(%q) returned empty string", input)
			}

			// Invariant 2: result contains no null bytes
			if strings.ContainsRune(got, '\x00') {
				t.Errorf("SanitizeFilename(%q) = %q contains null byte", input, got)
			}

			// Invariant 3: result contains no path separators
			if strings.ContainsRune(got, '/') {
				t.Errorf("SanitizeFilename(%q) = %q contains forward slash", input, got)
			}
			if strings.ContainsRune(got, '\\') {
				t.Errorf("SanitizeFilename(%q) = %q contains backslash", input, got)
			}

			// Invariant 4: result does not start with '-'
			if len(got) > 0 && got[0] == '-' {
				t.Errorf("SanitizeFilename(%q) = %q starts with dash", input, got)
			}

			// Invariant 5: result does not start with '@'
			if len(got) > 0 && got[0] == '@' {
				t.Errorf("SanitizeFilename(%q) = %q starts with @", input, got)
			}

			// Invariant 6: result does not exceed maxFilenameBytes
			if len(got) > maxFilenameBytes {
				t.Errorf("SanitizeFilename(%q) = %q has %d bytes, exceeds %d",
					input, got, len(got), maxFilenameBytes)
			}

			// Invariant 7: result is valid UTF-8
			if !utf8.ValidString(got) {
				t.Errorf("SanitizeFilename(%q) = %q is not valid UTF-8", input, got)
			}

			// Invariant 8: result contains no C0 control characters or DEL
			for _, r := range got {
				if r <= 0x1F || r == 0x7F {
					t.Errorf("SanitizeFilename(%q) = %q contains control character U+%04X",
						input, got, r)
				}
			}

			// Invariant 9: result contains no shell metacharacters
			for _, r := range got {
				switch r {
				case '\'', '"', '`', '$', '!', ';', '&', '|',
					'(', ')', '{', '}', '[', ']', '<', '>':
					t.Errorf("SanitizeFilename(%q) = %q contains shell metacharacter %q",
						input, got, string(r))
				}
			}

			// Invariant 10: result contains no dangerous Unicode control chars
			for _, r := range got {
				switch r {
				case '\u200B', '\u200C', '\u200D', '\u200E', '\u200F',
					'\u202A', '\u202B', '\u202C', '\u202D', '\u202E',
					'\u2060', '\u2061', '\u2062', '\u2063', '\u2064',
					'\uFEFF', '\uFFF9', '\uFFFA', '\uFFFB':
					t.Errorf("SanitizeFilename(%q) = %q contains Unicode control char U+%04X",
						input, got, r)
				}
			}

			// Invariant 11: result is not "." or ".."
			if got == "." || got == ".." {
				t.Errorf("SanitizeFilename(%q) = %q is a dot traversal", input, got)
			}
		})
	}
}

// TestSanitizeFilenameWindowsReservedExhaustive tests all Windows reserved names.
func TestSanitizeFilenameWindowsReservedExhaustive(t *testing.T) {
	reserved := []string{
		"CON", "PRN", "AUX", "NUL",
		"COM0", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT0", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
	}

	for _, name := range reserved {
		// Test bare name
		t.Run(name, func(t *testing.T) {
			got := SanitizeFilename(name)
			if got == name {
				t.Errorf("SanitizeFilename(%q) = %q, should have been prefixed", name, got)
			}
			if !strings.HasPrefix(got, "_") {
				t.Errorf("SanitizeFilename(%q) = %q, expected underscore prefix", name, got)
			}
		})

		// Test with extension
		t.Run(name+".txt", func(t *testing.T) {
			input := name + ".txt"
			got := SanitizeFilename(input)
			if got == input {
				t.Errorf("SanitizeFilename(%q) = %q, should have been prefixed", input, got)
			}
		})

		// Test lowercase
		t.Run("lower_"+name, func(t *testing.T) {
			lower := strings.ToLower(name)
			got := SanitizeFilename(lower)
			if got == lower {
				t.Errorf("SanitizeFilename(%q) = %q, should have been prefixed", lower, got)
			}
		})

		// Test mixed case
		t.Run("mixed_"+name, func(t *testing.T) {
			// Title case: first letter upper, rest lower
			mixed := string(name[0]) + strings.ToLower(name[1:])
			got := SanitizeFilename(mixed)
			if got == mixed {
				t.Errorf("SanitizeFilename(%q) = %q, should have been prefixed", mixed, got)
			}
		})
	}

	// Test names that should NOT be treated as reserved
	notReserved := []string{
		"CONX", "PRNT", "AUXX", "NULL",
		"COM10", "COM", "LPT", "LPTA",
		"CONNECT", "PRINTER", "AUXILIARY",
	}

	for _, name := range notReserved {
		t.Run("not_reserved_"+name, func(t *testing.T) {
			got := SanitizeFilename(name)
			if strings.HasPrefix(got, "_") {
				t.Errorf("SanitizeFilename(%q) = %q, should NOT have been prefixed (not a reserved name)", name, got)
			}
		})
	}
}

// TestSanitizeFilenameIdempotent verifies that sanitizing an already-sanitized
// filename produces the same result (idempotency).
func TestSanitizeFilenameIdempotent(t *testing.T) {
	inputs := []string{
		"report.pdf",
		"../../etc/passwd",
		"file;rm -rf /",
		"CON.txt",
		"-rf",
		"@filelist",
		"\u202Efdp.exe",
		strings.Repeat("x", 300),
		".hidden",
		"file\x00name.txt",
	}

	for _, input := range inputs {
		t.Run(sanitizeTestName(input), func(t *testing.T) {
			first := SanitizeFilename(input)
			second := SanitizeFilename(first)
			if first != second {
				t.Errorf("not idempotent: SanitizeFilename(%q) = %q, but SanitizeFilename(%q) = %q",
					input, first, first, second)
			}
		})
	}
}

// sanitizeTestName creates a safe test name from arbitrary input.
func sanitizeTestName(s string) string {
	if len(s) > 40 {
		s = s[:40] + "..."
	}
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	result := b.String()
	if result == "" {
		return "empty"
	}
	return result
}
