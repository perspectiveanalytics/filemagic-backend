package converter

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/perspectiveanalytics/filemagic-backend/internal/config"
)

type NsjailRunner struct {
	cfg *config.Config
}

// requiredConfigs lists every nsjail profile the converters depend on.
// Each entry must have a matching _seccomp.policy file.
var requiredConfigs = []string{
	"archive.cfg",
	"cert.cfg",
	"ebook.cfg",
	"ffmpeg.cfg",
	"font.cfg",
	"image.cfg",
	"image_to_pdf.cfg",
	"metadata.cfg",
	"ocr.cfg",
	"pandoc.cfg",
	"pdf.cfg",
	"poppler.cfg",
	"pymupdf.cfg",
	"qpdf.cfg",
	"svg.cfg",
}

func NewNsjailRunner(cfg *config.Config) *NsjailRunner {
	return &NsjailRunner{cfg: cfg}
}

// ValidateConfigs checks that all required nsjail config and seccomp policy
// files exist. Call at startup to fail fast instead of at first job.
func (n *NsjailRunner) ValidateConfigs() error {
	for _, cfgFile := range requiredConfigs {
		configPath := filepath.Join(n.cfg.NsjailConfigDir, cfgFile)
		if _, err := os.Stat(configPath); err != nil {
			return fmt.Errorf("nsjail config missing: %s", configPath)
		}

		seccompFile := strings.TrimSuffix(cfgFile, ".cfg") + "_seccomp.policy"
		seccompPath := filepath.Join(n.cfg.NsjailConfigDir, seccompFile)
		if _, err := os.Stat(seccompPath); err != nil {
			return fmt.Errorf("seccomp policy missing: %s", seccompPath)
		}
	}
	return nil
}

// Run executes a command inside nsjail.
// Commands should use absolute host paths — paths under jobDir are
// automatically rewritten to /work/... inside the sandbox.
func (n *NsjailRunner) Run(ctx context.Context, command []string, jobDir string, configFile string, timeout time.Duration) error {
	return n.executeNsjail(ctx, command, jobDir, configFile, timeout)
}

func (n *NsjailRunner) executeNsjail(ctx context.Context, command []string, jobDir string, configFile string, timeout time.Duration) error {
	if timeout == 0 {
		timeout = n.cfg.JobTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Rewrite host paths to jail paths
	jailCmd := make([]string, len(command))
	for i, arg := range command {
		jailCmd[i] = strings.Replace(arg, jobDir, "/work", 1)
	}

	configPath := filepath.Join(n.cfg.NsjailConfigDir, configFile)

	// Resolve config path to absolute to ensure seccomp_policy_file resolves correctly
	absConfigPath, err := filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("failed to resolve config path: %w", err)
	}

	// Refuse to run if the nsjail config or seccomp policy is missing.
	if _, err := os.Stat(absConfigPath); err != nil {
		return fmt.Errorf("nsjail config missing: %s", absConfigPath)
	}

	seccompFile := strings.TrimSuffix(configFile, ".cfg") + "_seccomp.policy"
	seccompPath := filepath.Join(filepath.Dir(absConfigPath), seccompFile)

	if _, err := os.Stat(seccompPath); err != nil {
		return fmt.Errorf("seccomp policy missing for %s (expected %s)", configFile, seccompPath)
	}

	args := []string{
		"--config", absConfigPath,
		"--bindmount_ro", fmt.Sprintf("%s:/work", jobDir),
		"--bindmount", fmt.Sprintf("%s/output:/work/output", jobDir),
		"--quiet",
		"--seccomp_policy", seccompPath,
	}

	// Add cgroupv2 resource limits when delegation is available.
	if n.cfg.Cgroupv2Mount != "" {
		memMax, pidsMax := cgroupLimits(configFile)
		if memMax > 0 && pidsMax > 0 {
			args = append(args,
				"--detect_cgroupv2",
				"--cgroupv2_mount", n.cfg.Cgroupv2Mount,
				"--cgroup_mem_max", fmt.Sprintf("%d", memMax),
				"--cgroup_pids_max", fmt.Sprintf("%d", pidsMax),
			)
		}
	}

	args = append(args, "--")
	args = append(args, jailCmd...)

	cmd := exec.CommandContext(ctx, n.cfg.NsjailPath, args...)
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = []string{}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	slog.Info("executing nsjail",
		"config", configFile,
		"command", redactCommand(jailCmd),
		"timeout", timeout,
	)

	if err = cmd.Start(); err != nil {
		return fmt.Errorf("failed to start nsjail: %w", err)
	}

	err = waitOrKill(ctx, cmd, timeout)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("conversion timed out after %v", timeout)
		}
		slog.Error("nsjail execution failed",
			"error", err,
			"stderr", stderr.String(),
		)
		return fmt.Errorf("conversion failed: %w", err)
	}

	return nil
}

// RunWithOutput executes a command inside nsjail and returns its stdout.
func (n *NsjailRunner) RunWithOutput(ctx context.Context, command []string, jobDir string, configFile string, timeout time.Duration) ([]byte, error) {
	return n.executeNsjailWithOutput(ctx, command, jobDir, configFile, timeout)
}

func (n *NsjailRunner) executeNsjailWithOutput(ctx context.Context, command []string, jobDir string, configFile string, timeout time.Duration) ([]byte, error) {
	if timeout == 0 {
		timeout = n.cfg.JobTimeout
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	jailCmd := make([]string, len(command))
	for i, arg := range command {
		jailCmd[i] = strings.Replace(arg, jobDir, "/work", 1)
	}

	configPath := filepath.Join(n.cfg.NsjailConfigDir, configFile)
	absConfigPath, err := filepath.Abs(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve config path: %w", err)
	}

	if _, err := os.Stat(absConfigPath); err != nil {
		return nil, fmt.Errorf("nsjail config missing: %s", absConfigPath)
	}

	seccompFile := strings.TrimSuffix(configFile, ".cfg") + "_seccomp.policy"
	seccompPath := filepath.Join(filepath.Dir(absConfigPath), seccompFile)

	if _, err := os.Stat(seccompPath); err != nil {
		return nil, fmt.Errorf("seccomp policy missing for %s (expected %s)", configFile, seccompPath)
	}

	args := []string{
		"--config", absConfigPath,
		"--bindmount_ro", fmt.Sprintf("%s:/work", jobDir),
		"--bindmount", fmt.Sprintf("%s/output:/work/output", jobDir),
		"--quiet",
		"--seccomp_policy", seccompPath,
	}

	if n.cfg.Cgroupv2Mount != "" {
		memMax, pidsMax := cgroupLimits(configFile)
		if memMax > 0 && pidsMax > 0 {
			args = append(args,
				"--detect_cgroupv2",
				"--cgroupv2_mount", n.cfg.Cgroupv2Mount,
				"--cgroup_mem_max", fmt.Sprintf("%d", memMax),
				"--cgroup_pids_max", fmt.Sprintf("%d", pidsMax),
			)
		}
	}

	args = append(args, "--")
	args = append(args, jailCmd...)

	cmd := exec.CommandContext(ctx, n.cfg.NsjailPath, args...)
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = []string{}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	slog.Info("executing nsjail (with output)",
		"config", configFile,
		"command", redactCommand(jailCmd),
		"timeout", timeout,
	)

	if err = cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start nsjail: %w", err)
	}

	err = waitOrKill(ctx, cmd, timeout)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("conversion timed out after %v", timeout)
		}
		slog.Error("nsjail execution failed",
			"error", err,
			"stderr", stderr.String(),
		)
		return nil, fmt.Errorf("conversion failed: %w", err)
	}

	return stdout.Bytes(), nil
}

// waitOrKill waits for cmd to finish. If the context expires and cmd.Wait
// does not return within a grace period, it sends SIGKILL directly to the
// process to handle cases where the context-triggered kill fails to reap
// a stuck nsjail (e.g. libheif thread deadlocks).
func waitOrKill(ctx context.Context, cmd *exec.Cmd, timeout time.Duration) error {
	const killGrace = 5 * time.Second

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		// Context expired (timeout or cancellation). cmd.Wait may already
		// be returning because exec.CommandContext sends SIGKILL on cancel.
		// Give it a moment before forcing a second kill.
		select {
		case err := <-done:
			return err
		case <-time.After(killGrace):
			if cmd.Process != nil {
				slog.Warn("nsjail did not exit after context cancel, sending SIGKILL",
					"pid", cmd.Process.Pid,
					"timeout", timeout,
				)
				cmd.Process.Kill()
			}
			return <-done
		}
	}
}

func PrepareJobDir(basePath, jobID string) (string, string, string, error) {
	jobDir := filepath.Join(basePath, jobID)
	inputDir := filepath.Join(jobDir, "input")
	outputDir := filepath.Join(jobDir, "output")

	if err := os.MkdirAll(inputDir, 0755); err != nil {
		return "", "", "", fmt.Errorf("failed to create input dir: %w", err)
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", "", "", fmt.Errorf("failed to create output dir: %w", err)
	}

	return jobDir, inputDir, outputDir, nil
}

func CleanupJobDir(basePath, jobID string) error {
	jobDir := filepath.Join(basePath, jobID)
	return os.RemoveAll(jobDir)
}

// redactCommand returns a copy of cmd with password arguments replaced by [REDACTED].
// Handles: 7z's -p<password>, qpdf's --password=<value>, qpdf's --encrypt <user> <owner> <bits>.
func redactCommand(cmd []string) []string {
	redacted := make([]string, len(cmd))
	copy(redacted, cmd)
	skipNext := 0
	for i, arg := range redacted {
		if skipNext > 0 {
			redacted[i] = "[REDACTED]"
			skipNext--
			continue
		}
		// 7z: -p<password> (always concatenated, len > 2 distinguishes from bare -p)
		if strings.HasPrefix(arg, "-p") && len(arg) > 2 && arg[2] != '-' {
			redacted[i] = "-p[REDACTED]"
		}
		// qpdf: --password=<value>
		if strings.HasPrefix(arg, "--password=") {
			redacted[i] = "--password=[REDACTED]"
		}
		// qpdf: --encrypt <user_pass> <owner_pass> <bits>
		if arg == "--encrypt" && i+2 < len(redacted) {
			skipNext = 2
		}
	}
	return redacted
}

// cgroupLimits returns (memoryMaxBytes, pidsMax) for the given config profile.
func cgroupLimits(configFile string) (uint64, uint64) {
	switch configFile {
	case "pdf.cfg":
		// Ghostscript PDF processing: needs more memory
		return 1024 * 1024 * 1024, 32 // 1 GB, 32 PIDs
	case "image.cfg":
		// Disabled: nsjail cgroup management (creating cgroups, setting
		// mem/pids limits) causes intermittent thread deadlocks in vips'
		// HEIC decoder (libheif/libde265). Resource containment is still
		// provided by rlimits (rlimit_as=1GB, rlimit_cpu=120s,
		// rlimit_nproc=32), seccomp policy, and namespace isolation.
		return 0, 0
	case "ocr.cfg":
		// Tesseract OCR: needs memory for language models and large images
		return 1024 * 1024 * 1024, 32 // 1 GB, 32 PIDs
	case "image_to_pdf.cfg":
		// ImageMagick image-to-PDF: can be memory-hungry with large images
		return 1024 * 1024 * 1024, 32 // 1 GB, 32 PIDs
	case "metadata.cfg":
		// exiftool metadata removal: lightweight Perl script
		return 256 * 1024 * 1024, 16 // 256 MB, 16 PIDs
	case "cert.cfg":
		// OpenSSL certificate operations: lightweight
		return 256 * 1024 * 1024, 16 // 256 MB, 16 PIDs
	case "archive.cfg":
		// 7z/tar archive operations: moderate memory for compression
		return 512 * 1024 * 1024, 16 // 512 MB, 16 PIDs
	case "pandoc.cfg":
		// Pandoc markdown to PDF: needs memory for LaTeX/PDF engine
		return 512 * 1024 * 1024, 32 // 512 MB, 32 PIDs
	case "ffmpeg.cfg":
		// FFmpeg audio/video processing: needs memory for demuxing
		return 2048 * 1024 * 1024, 32 // 2 GB, 32 PIDs
	case "qpdf.cfg":
		return 256 * 1024 * 1024, 16 // 256 MB, 16 PIDs
	case "svg.cfg":
		return 256 * 1024 * 1024, 16 // 256 MB, 16 PIDs
	case "poppler.cfg":
		return 512 * 1024 * 1024, 32 // 512 MB, 32 PIDs
	case "pymupdf.cfg":
		return 512 * 1024 * 1024, 8 // 512 MB, 8 PIDs
	case "font.cfg":
		// Python fonttools: lightweight font conversion
		return 512 * 1024 * 1024, 8 // 512 MB, 8 PIDs
	case "ebook.cfg":
		// Calibre ebook-convert: heavier, needs memory for PDF output
		return 1024 * 1024 * 1024, 32 // 1 GB, 32 PIDs
	default:
		return 256 * 1024 * 1024, 16 // 256 MB, 16 PIDs
	}
}
