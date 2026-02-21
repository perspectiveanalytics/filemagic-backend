package cgroup

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Setup performs cgroupv2 delegation for nsjail.
// When running under a systemd service with Delegate=yes, the service's cgroup
// subtree is owned by the service user. This function:
// 1. Reads the process's current cgroup from /proc/self/cgroup
// 2. Creates a "service" sub-cgroup and migrates this process into it
//    (cgroupv2 requires no processes in cgroups that have children with controllers)
// 3. Enables memory+pids+cpu controllers via subtree_control
// 4. Returns the delegated cgroup path for nsjail to use as cgroupv2_mount
func Setup() (string, error) {
	cgroupPath, err := selfCgroupPath()
	if err != nil {
		return "", fmt.Errorf("failed to read cgroup: %w", err)
	}

	cgroupFS := "/sys/fs/cgroup"

	// Verify this is cgroupv2
	var st syscall.Statfs_t
	if err := syscall.Statfs(cgroupFS, &st); err != nil {
		return "", fmt.Errorf("failed to statfs %s: %w", cgroupFS, err)
	}
	// CGROUP2_SUPER_MAGIC = 0x63677270
	if st.Type != 0x63677270 {
		return "", fmt.Errorf("not a cgroupv2 filesystem at %s", cgroupFS)
	}

	fullCgroupPath := filepath.Join(cgroupFS, cgroupPath)

	// Verify we own this cgroup (systemd Delegate=yes + User= chowns it to us)
	info, err := os.Stat(fullCgroupPath)
	if err != nil {
		return "", fmt.Errorf("failed to stat cgroup %s: %w", fullCgroupPath, err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	if stat.Uid != uint32(os.Getuid()) {
		return "", fmt.Errorf("cgroup %s not owned by us (uid %d, want %d) — is Delegate=yes set in the systemd service?",
			fullCgroupPath, stat.Uid, os.Getuid())
	}

	// Create a "service" sub-cgroup for our own process
	serviceCgroup := filepath.Join(fullCgroupPath, "service")
	if err := os.MkdirAll(serviceCgroup, 0755); err != nil {
		return "", fmt.Errorf("failed to create service cgroup: %w", err)
	}

	// Move ourselves into the service sub-cgroup
	// (cgroupv2 "no internal processes" constraint: a cgroup with controllers
	// enabled for children cannot have its own processes)
	pid := fmt.Sprintf("%d", os.Getpid())
	if err := os.WriteFile(filepath.Join(serviceCgroup, "cgroup.procs"), []byte(pid), 0644); err != nil {
		return "", fmt.Errorf("failed to migrate to service cgroup: %w", err)
	}

	// Enable controllers for child cgroups (nsjail will create them)
	controllers := "+memory +pids +cpu"
	subtreeControl := filepath.Join(fullCgroupPath, "cgroup.subtree_control")
	if err := os.WriteFile(subtreeControl, []byte(controllers), 0644); err != nil {
		return "", fmt.Errorf("failed to enable cgroup controllers in %s: %w", subtreeControl, err)
	} else {
		slog.Info("cgroupv2 delegation setup complete",
			"cgroup", fullCgroupPath,
			"controllers", controllers,
		)
	}

	return fullCgroupPath, nil
}

// selfCgroupPath reads the current process's cgroup from /proc/self/cgroup.
// On cgroupv2 (unified hierarchy), there's a single line: "0::/path"
func selfCgroupPath() (string, error) {
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", err
	}

	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		// cgroupv2 format: "0::/path/to/cgroup"
		parts := strings.SplitN(line, ":", 3)
		if len(parts) == 3 && parts[0] == "0" && parts[1] == "" {
			return parts[2], nil
		}
	}

	return "", fmt.Errorf("no cgroupv2 entry found in /proc/self/cgroup")
}
