package config

import (
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddr       string
	TmpfsPath        string
	MaxFileSize      int64
	MaxQueueSize     int
	JobTimeout       time.Duration
	CleanupInterval  time.Duration
	CleanupMaxAge    time.Duration
	NsjailPath       string
	NsjailConfigDir  string
	RateLimitRPM     int
	RateLimitRPH     int
	CORSOrigin       string
	TurnstileSecret  string
	ContactEmail     string
	TrustedIPs       map[string]bool

	// Cloudflare KV for optional stats counters (files processed, thanks).
	CFAccountID   string
	CFNamespaceID string
	CFApiToken    string

	// Cgroupv2Mount is set at runtime by cgroup.Setup() when delegation is available.
	// When set, nsjail uses this path for cgroupv2 resource limits.
	Cgroupv2Mount string
}

func Load() *Config {
	return &Config{
		ListenAddr:       getEnv("FILEMAGIC_LISTEN_ADDR", ":8090"),
		TmpfsPath:        getEnv("FILEMAGIC_TMPFS_PATH", "/mnt/memdir/filemagic"),
		MaxFileSize:      getEnvInt64("FILEMAGIC_MAX_FILE_SIZE", 20*1024*1024),
		MaxQueueSize:     getEnvInt("FILEMAGIC_MAX_QUEUE_SIZE", 15),
		JobTimeout:       time.Duration(getEnvInt("FILEMAGIC_JOB_TIMEOUT", 120)) * time.Second,
		CleanupInterval:  time.Duration(getEnvInt("FILEMAGIC_CLEANUP_INTERVAL", 60)) * time.Second,
		CleanupMaxAge:    time.Duration(getEnvInt("FILEMAGIC_CLEANUP_MAX_AGE", 600)) * time.Second,
		NsjailPath:       getEnv("FILEMAGIC_NSJAIL_PATH", "/usr/bin/nsjail"),
		NsjailConfigDir:  getEnv("FILEMAGIC_NSJAIL_CONFIG_DIR", "./configs/nsjail"),
		RateLimitRPM:     getEnvInt("FILEMAGIC_RATE_LIMIT_RPM", 10),
		RateLimitRPH:     getEnvInt("FILEMAGIC_RATE_LIMIT_RPH", 60),
		CORSOrigin:       getEnv("FILEMAGIC_CORS_ORIGIN", ""),
		TurnstileSecret:  getEnv("FILEMAGIC_TURNSTILE_SECRET", ""),
		ContactEmail:     getEnv("FILEMAGIC_CONTACT_EMAIL", ""),
		TrustedIPs:       parseTrustedIPs(os.Getenv("FILEMAGIC_TRUSTED_IPS")),
		CFAccountID:      getEnv("FILEMAGIC_CF_ACCOUNT_ID", ""),
		CFNamespaceID:    getEnv("FILEMAGIC_CF_NAMESPACE_ID", ""),
		CFApiToken:       getEnv("FILEMAGIC_CF_API_TOKEN", ""),
	}
}

func getEnv(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}

func getEnvInt64(key string, defaultVal int64) int64 {
	if val := os.Getenv(key); val != "" {
		if i, err := strconv.ParseInt(val, 10, 64); err == nil {
			return i
		}
	}
	return defaultVal
}

func parseTrustedIPs(val string) map[string]bool {
	m := make(map[string]bool)
	if val == "" {
		return m
	}
	for _, ip := range strings.Split(val, ",") {
		if ip = strings.TrimSpace(ip); ip != "" {
			m[ip] = true
		}
	}
	return m
}
