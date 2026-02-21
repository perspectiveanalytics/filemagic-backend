# FileMagic - Backend

Backend service for [filemagic.app](https://filemagic.app), a free and private file conversion platform. All file processing happens in memory on a tmpfs mount. Nothing touches disk, nothing is stored.

## Disclaimer

This software is provided "as is", without warranty of any kind. The authors are not liable for any damages, data loss, or security incidents arising from its use. Converted files are not checked for correctness. Do not use this as the sole tool for anything critical without verifying the output independently. Always keep a copy of your original files.

### Tools used

FFmpeg, libvips, ImageMagick, Ghostscript, Tesseract, Pandoc, ExifTool, OpenSSL, QPDF, Calibre, 7-Zip. Each runs inside its own nsjail profile with a minimal seccomp policy.

## Configuration

All configuration is via environment variables:

| Variable | Default | Description |
|---|---|---|
| `FILEMAGIC_LISTEN_ADDR` | `:8090` | Listen address |
| `FILEMAGIC_TMPFS_PATH` | `/mnt/memdir/filemagic` | tmpfs mount for file processing |
| `FILEMAGIC_NSJAIL_PATH` | `/usr/bin/nsjail` | Path to nsjail binary |
| `FILEMAGIC_NSJAIL_CONFIG_DIR` | `./configs/nsjail` | nsjail config directory |
| `FILEMAGIC_MAX_QUEUE_SIZE` | `15` | Max concurrent jobs |
| `FILEMAGIC_JOB_TIMEOUT` | `120s` | Per-job timeout |
| `FILEMAGIC_RATE_LIMIT_RPM` | `10` | Requests per minute per IP |
| `FILEMAGIC_RATE_LIMIT_RPH` | `60` | Requests per hour per IP |
| `FILEMAGIC_CORS_ORIGIN` | | Allowed CORS origin |
| `FILEMAGIC_TURNSTILE_SECRET` | | Cloudflare Turnstile secret key |
| `FILEMAGIC_TRUSTED_IPS` | | Comma-separated IPs that bypass Turnstile |
| `FILEMAGIC_CONTACT_EMAIL` | | Contact form recipient |
| `FILEMAGIC_CF_ACCOUNT_ID` | | Cloudflare account ID (optional, for stats) |
| `FILEMAGIC_CF_NAMESPACE_ID` | | Cloudflare KV namespace ID (optional) |
| `FILEMAGIC_CF_API_TOKEN` | | Cloudflare API token (optional) |

## Security model

Every file conversion runs inside an **nsjail** sandbox with:

- Dedicated seccomp-bpf policy per tool (whitelist approach)
- Read-only root filesystem, writable only in `/work`
- cgroup v2 CPU and memory limits
- No network access
- PID and mount namespace isolation
- Files processed on tmpfs, never written to persistent storage

See [filemagic.app/security](https://filemagic.app/security) for the full writeup.

## Tests

```bash
# Unit tests
CGO_ENABLED=1 go test -race ./internal/...

# Integration tests (requires running server + system tools)
CGO_ENABLED=1 go test -v -tags integration -timeout 10m ./tests/integration/...
```

## License

AGPL-3.0
