# Installation

This documents the full deployment of FileMagic on a dedicated VM. The setup covers OS hardening, sandboxing and service configuration.

## Disclaimer

This guide is provided for reference only. If you deploy this software, you are solely responsible for the security and maintenance of your infrastructure. The authors make no guarantees about the completeness or correctness of these instructions, and accept no liability for security incidents, data loss, or any damages resulting from following them. You should review and adapt every configuration to your own environment and threat model before putting anything in production.

## Architecture

Traffic flows through three layers:

```
User -> Cloudflare (CDN + WAF + Turnstile) -> Cloudflare Tunnel -> Reverse proxy -> VM
```

The reverse proxy terminates the tunnel and forwards requests to the VM. The backend listens on `:8090`, the frontend runs as a Docker container on `:8080`. Neither is exposed to the internet directly.

The VM should sit in an isolated VLAN. A perimeter firewall (separate from the host's nftables) should restrict outbound traffic to only what the service actually needs:

| Destination | Port | Purpose |
|---|---|---|
| Ubuntu repos (`archive.ubuntu.com`, `security.ubuntu.com`) | 443 | OS updates |
| `ntp.ubuntu.com` | 123/udp | Time sync |
| `challenges.cloudflare.com` | 443 | Turnstile verification |
| `download.docker.com` | 443 | Docker images/updates |
| `github.com`, `objects.githubusercontent.com` | 443 | nsjail source, Pandoc .deb (for installation only) |
| DNS resolver | 53 | Name resolution |

Everything else should be dropped. The goal is to limit blast radius if the VM is compromised: no lateral movement, no unexpected outbound connections.

## Base OS

Ubuntu 24.04 LTS. After initial setup (hostname, network, SSH keys), strip the default bloat:

```bash
apt purge -y apport cloud-init snapd landscape-common ubuntu-pro-client \
  popularity-contest lxd-installer modemmanager postfix fwupd plymouth \
  open-vm-tools packagekit sosreport thermald update-manager-core \
  software-properties-common ssh-import-id usb-modeswitch wireless-regdb \
  byobu screen command-not-found motd-news-config multipath-tools \
  open-iscsi ntfs-3g os-prober pastebinit pollinate squashfs-tools bolt
apt autoremove -y && apt clean
```

Keep `xfsprogs` and `lvm2` if your disk layout needs them.

## Kernel hardening

### Boot parameters

```bash
GRUB_CMDLINE_LINUX_DEFAULT="console=tty0 console=ttyS0,115200n8 \
  slab_nomerge init_on_alloc=1 init_on_free=1 page_alloc.shuffle=1 \
  randomize_kstack_offset=on pti=on vsyscall=none debugfs=off \
  oops=panic module.sig_enforce=1 lockdown=confidentiality \
  iommu.passthrough=0 iommu.strict=1 \
  quiet loglevel=0"
```

After editing `/etc/default/grub`, run `update-grub` and reboot.

### Sysctls

Create `/etc/sysctl.d/99-hardening.conf`.

```ini
# Kernel self-protection
kernel.kptr_restrict=2
kernel.dmesg_restrict=1
kernel.printk=3 3 3 3
kernel.unprivileged_bpf_disabled=1
net.core.bpf_jit_harden=2
kernel.perf_event_paranoid=3
kernel.yama.ptrace_scope=2
kernel.kexec_load_disabled=1
dev.tty.ldisc_autoload=0
vm.unprivileged_userfaultfd=0
kernel.sysrq=0
vm.mmap_rnd_bits=32
vm.mmap_rnd_compat_bits=16
vm.mmap_min_addr=65536

# Network
net.ipv4.tcp_syncookies=1
net.ipv4.conf.all.rp_filter=1
net.ipv4.conf.default.rp_filter=1
net.ipv4.conf.all.accept_redirects=0
net.ipv4.conf.default.accept_redirects=0
net.ipv4.conf.all.secure_redirects=0
net.ipv4.conf.default.secure_redirects=0
net.ipv6.conf.all.accept_redirects=0
net.ipv6.conf.default.accept_redirects=0
net.ipv4.conf.all.send_redirects=0
net.ipv4.conf.default.send_redirects=0
net.ipv4.icmp_echo_ignore_all=1
net.ipv4.conf.all.accept_source_route=0
net.ipv4.conf.default.accept_source_route=0
net.ipv6.conf.all.accept_source_route=0
net.ipv6.conf.default.accept_source_route=0
net.ipv6.conf.all.accept_ra=0
net.ipv6.conf.default.accept_ra=0
net.ipv4.conf.all.log_martians=1
net.ipv4.conf.default.log_martians=1
net.ipv6.conf.all.forwarding=0

# Core dumps
fs.suid_dumpable=0
kernel.core_pattern=|/bin/false

# Filesystem protections
fs.protected_symlinks=1
fs.protected_hardlinks=1
fs.protected_fifos=2
fs.protected_regular=2
```

Apply with `sysctl --system`.

## SSH hardening

Regenerate host keys (drop DSA and ECDSA):

```bash
cd /etc/ssh
rm ssh_host_*
ssh-keygen -t ed25519 -f ssh_host_ed25519_key -N "" -q
ssh-keygen -t rsa -b 4096 -f ssh_host_rsa_key -N "" -q
awk '$5 >= 3071' /etc/ssh/moduli > /etc/ssh/moduli.safe
mv /etc/ssh/moduli.safe /etc/ssh/moduli
```

Create `/etc/ssh/sshd_config.d/hardening.conf`.

```
Port 22
AddressFamily inet

HostKey /etc/ssh/ssh_host_ed25519_key
HostKey /etc/ssh/ssh_host_rsa_key

# Post-quantum KEX (sntrup761 = hybrid NTRU Prime + X25519)
KexAlgorithms sntrup761x25519-sha512@openssh.com,curve25519-sha256,curve25519-sha256@libssh.org,diffie-hellman-group18-sha512,diffie-hellman-group16-sha512
Ciphers chacha20-poly1305@openssh.com,aes256-gcm@openssh.com,aes256-ctr
MACs hmac-sha2-512-etm@openssh.com,hmac-sha2-256-etm@openssh.com,umac-128-etm@openssh.com

HostKeyAlgorithms ssh-ed25519,ssh-ed25519-cert-v01@openssh.com,rsa-sha2-512,rsa-sha2-512-cert-v01@openssh.com,rsa-sha2-256,rsa-sha2-256-cert-v01@openssh.com
PubkeyAcceptedAlgorithms ssh-ed25519,ssh-ed25519-cert-v01@openssh.com,rsa-sha2-512,rsa-sha2-512-cert-v01@openssh.com,rsa-sha2-256,rsa-sha2-256-cert-v01@openssh.com
RequiredRSASize 3072

PermitRootLogin prohibit-password
PubkeyAuthentication yes
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitEmptyPasswords no
AuthenticationMethods publickey
HostbasedAuthentication no
IgnoreRhosts yes

LoginGraceTime 30
MaxAuthTries 3
MaxSessions 3
MaxStartups 3:50:10

AllowTcpForwarding no
AllowStreamLocalForwarding no
X11Forwarding no
AllowAgentForwarding no
GatewayPorts no
PermitTunnel no
DisableForwarding yes

ClientAliveInterval 300
ClientAliveCountMax 2
TCPKeepAlive no
UseDNS no

SyslogFacility AUTH
LogLevel VERBOSE
PermitUserEnvironment no
StrictModes yes
Compression no
DebianBanner no
VersionAddendum none

Banner /etc/ssh/banner
Subsystem sftp /usr/lib/openssh/sftp-server -f AUTHPRIV -l INFO
```

Validate with `sshd -t`, then `systemctl restart ssh`.

### fail2ban

```bash
apt install fail2ban
```

Create `/etc/fail2ban/jail.d/sshd-hardened.conf`.

```ini
[sshd]
enabled  = true
port     = ssh
filter   = sshd
logpath  = /var/log/auth.log
backend  = systemd
maxretry = 3
findtime = 3600
bantime  = 86400

[recidive]
enabled  = true
logpath  = /var/log/fail2ban.log
banaction = %(banaction_allports)s
maxretry = 3
findtime = 86400
bantime  = 604800
```

## Firewall

The VM sits behind a reverse proxy, so inbound filtering is handled upstream. The nftables config blocks lateral movement on the local subnet (todo only if you have other VMs in the same VLAN used as DMZ).

`/etc/nftables.conf`.
```
#!/usr/sbin/nft -f

table inet filter {
    chain input { type filter hook input priority filter; }
    chain forward { type filter hook forward priority filter; }
    chain output { type filter hook output priority filter; }
}

include "/etc/nftables.d/*.conf"
```

`/etc/nftables.d/custom-firewall.conf`.
```
table ip custom-firewall {
    chain output {
        type filter hook output priority filter; policy accept;
        ip daddr <GATEWAY_IP> accept comment "allow-gateway"
        ip daddr <LOCAL_SUBNET>/24 counter drop comment "block-local-subnet"
    }
}
```

Enable with `systemctl enable --now nftables`.

## System tools

Install everything the backend needs for file conversions. Adapt the version numbers and packages names as the doc may be old when you will read it.

```bash
# Build nsjail from source
apt install -y git make gcc g++ flex bison libprotobuf-dev \
  protobuf-compiler libnl-route-3-dev pkg-config
cd /tmp && git clone --recurse-submodules https://github.com/google/nsjail.git
cd nsjail && make -j$(nproc) && make install
rm -rf /tmp/nsjail

# Conversion tools
apt install -y libvips-tools ghostscript imagemagick \
  tesseract-ocr tesseract-ocr-eng tesseract-ocr-fra tesseract-ocr-deu \
  tesseract-ocr-spa tesseract-ocr-ita tesseract-ocr-por tesseract-ocr-nld \
  tesseract-ocr-rus tesseract-ocr-ara tesseract-ocr-chi-sim \
  tesseract-ocr-chi-tra tesseract-ocr-jpn tesseract-ocr-kor \
  libimage-exiftool-perl qpdf poppler-utils librsvg2-bin 7zip calibre

# ffmpeg without recommends (avoid pulling X11)
apt install --no-install-recommends ffmpeg

# Pandoc (grab the .deb from GitHub, apt version is usually outdated)
cd /tmp
wget https://github.com/jgm/pandoc/releases/download/3.9/pandoc-3.9-1-amd64.deb
dpkg -i pandoc-3.9-1-amd64.deb
```

## Users and permissions

```bash
# Service user (no login, no home)
useradd --system --no-create-home --shell /sbin/nologin filemagic
```

Directory layout:

```
/opt/filemagic-back/           # Binary + nsjail configs
/opt/filemagic-front/          # Docker Compose + frontend image
/etc/filemagic/env             # Environment variables (secrets)
```

Permissions:

```bash
chown root:filemagic /opt/filemagic-back /opt/filemagic-front
chmod 750 /opt/filemagic-back
chmod g+w /opt/filemagic-back /opt/filemagic-front
```

## tmpfs

All file processing happens in memory. Nothing touches persistent storage.

Add to `/etc/fstab`.

```
tmpfs /mnt/memdir tmpfs size=1G,mode=0700,uid=<FILEMAGIC_UID>,gid=<FILEMAGIC_GID>,noexec,nosuid,nodev 0 0
tmpfs /tmp tmpfs defaults,noexec,nosuid,nodev,size=512M 0 0
```

Then `mount -a` and create the working directory:

```bash
mkdir -p /mnt/memdir/filemagic
chown filemagic:filemagic /mnt/memdir/filemagic
```

## Docker (frontend only)

The frontend is a static React build served by nginx inside a container.

```bash
# Install Docker
apt install docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
```

`/opt/filemagic-front/docker-compose.yml`.

```yaml
services:
  filemagic-front:
    image: filemagic-front:latest
    container_name: filemagic-front
    restart: unless-stopped
    extra_hosts:
      - "host.docker.internal:host-gateway"
    ports:
      - "8080:8080"
    networks:
      frontend:
        ipv4_address: 172.30.0.10
    security_opt:
      - no-new-privileges:true
    cap_drop:
      - ALL
    cap_add:
      - CHOWN
      - SETUID
      - SETGID
      - DAC_OVERRIDE
    deploy:
      resources:
        limits:
          cpus: '0.5'
          memory: 256M
        reservations:
          cpus: '0.1'
          memory: 64M

networks:
  frontend:
    driver: bridge
    ipam:
      config:
        - subnet: 172.30.0.0/24
          gateway: 172.30.0.1
```

The container IP (`172.30.0.10`) is added to `FILEMAGIC_TRUSTED_IPS` so the frontend's health checks and internal requests bypass Turnstile verification.

## Backend service

### systemd unit

`/etc/systemd/system/filemagic.service`.

```ini
[Unit]
Description=FileMagic File Conversion Service
After=network.target

[Service]
Type=simple
User=filemagic
Group=filemagic
WorkingDirectory=/opt/filemagic-back

ExecStartPre=+/bin/mkdir -p /mnt/memdir/filemagic
ExecStartPre=+/bin/chown filemagic:filemagic /mnt/memdir/filemagic
ExecStart=/opt/filemagic-back/filemagic-back

# Environment
Environment=FILEMAGIC_USE_NSJAIL=true
Environment=FILEMAGIC_LISTEN_ADDR=:8090
Environment=FILEMAGIC_TMPFS_PATH=/mnt/memdir/filemagic
Environment=FILEMAGIC_NSJAIL_CONFIG_DIR=/opt/filemagic-back/configs/nsjail
Environment=FILEMAGIC_CORS_ORIGIN=https://filemagic.app
EnvironmentFile=/etc/filemagic/env

Restart=on-failure
RestartSec=5

# cgroup v2 delegation for nsjail
Delegate=cpu memory pids

# Resource limits
MemoryMax=2G
TasksMax=256
CPUQuota=200%

# Filesystem hardening
ProtectSystem=strict
ProtectHome=true
ReadWritePaths=/mnt/memdir /sys/fs/cgroup/system.slice/filemagic.service
PrivateTmp=true
PrivateDevices=true

# Network
RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK

# AppArmor
AppArmorProfile=filemagic-back

# Kernel hardening
ProtectKernelModules=true
ProtectKernelTunables=true
ProtectKernelLogs=true
ProtectControlGroups=false
ProtectClock=true
ProtectHostname=false
NoNewPrivileges=true
MemoryDenyWriteExecute=false
LockPersonality=true
RestrictRealtime=true
RestrictNamespaces=false
AmbientCapabilities=
CapabilityBoundingSet=CAP_SYS_ADMIN CAP_NET_ADMIN CAP_SETUID CAP_SETGID CAP_SYS_PTRACE

SystemCallArchitectures=native

StandardOutput=journal
StandardError=journal
SyslogIdentifier=filemagic

[Install]
WantedBy=multi-user.target
```

Then `systemctl daemon-reload && systemctl enable filemagic`.

### Environment file

`/etc/filemagic/env` holds the secrets. This file should be owned by `root:filemagic` with `640` permissions.

```bash
mkdir -p /etc/filemagic
chmod 750 /etc/filemagic
```

Contents:

```
FILEMAGIC_TURNSTILE_SECRET=<cloudflare-turnstile-secret>
FILEMAGIC_TRUSTED_IPS=<docker-container-ip>,<other-trusted-ips>
FILEMAGIC_CONTACT_EMAIL=<contact-email>
FILEMAGIC_CF_ACCOUNT_ID=<cloudflare-account-id>
FILEMAGIC_CF_NAMESPACE_ID=<cloudflare-kv-namespace-id>
FILEMAGIC_CF_API_TOKEN=<cloudflare-api-token>
```

#### Cloudflare KV token security

The `CF_API_TOKEN` is used to read and write counters in Cloudflare KV (file processing stats, thanks counter). When you create this token in the Cloudflare dashboard, restrict it by source IP to only allow requests from the server's public IP addresses. This way, even if the token leaks, it cannot be used from anywhere else.

## AppArmor

Ubuntu 24.04 restricts unprivileged user namespaces by default (`kernel.apparmor_restrict_unprivileged_userns=1`). Since nsjail relies on user namespaces, the Go binary needs an AppArmor profile that grants the `userns` permission.

Create `/etc/apparmor.d/opt.filemagic-back.filemagic-back` with a confining profile that lists every tool binary nsjail is allowed to execute (vips, gs, tesseract, ffmpeg, etc.), the tmpfs paths, cgroup delegation paths, and the system libraries that get bind-mounted into the jail. The profile uses `flags=(attach_disconnected)` because nsjail operates inside mount namespaces.

See the actual profile in the repository or on the server at `/etc/apparmor.d/opt.filemagic-back.filemagic-back`.

Load it with:

```bash
apparmor_parser -r /etc/apparmor.d/opt.filemagic-back.filemagic-back
```

## Scheduled tasks

```
CRON_TZ=UTC

# OS security upgrades (daily at 05:10 UTC)
10 5 * * * /usr/bin/bash /root/scripts/auto_os_upgrade.sh

# Docker cleanup
0 4 * * * /usr/bin/docker system prune -a -f
0 5 * * * /usr/bin/docker image prune -af

# Safety net: clean up stale job dirs older than 30 minutes
*/15 * * * * find /mnt/memdir/filemagic -mindepth 1 -maxdepth 1 -type d -mmin +30 -exec rm -rf {} + 2>/dev/null
```

If a reboot is required (new kernel, critical lib update), the auto upgrade script waits 60 seconds and reboots automatically.

## Verification

After everything is in place:

```bash
# Check the service is running
systemctl status filemagic
journalctl -fu filemagic

# Check AppArmor is enforcing
aa-status | grep filemagic

# Check nsjail works (should print the ExifTool version)
sudo -u filemagic aa-exec -p filemagic-back -- \
  nsjail --config /opt/filemagic-back/configs/nsjail/metadata.cfg \
  --bindmount /mnt/memdir/filemagic:/work --quiet \
  -- /usr/bin/exiftool -ver

# Check no AppArmor denials
dmesg -wT | grep DENIED

# Check the frontend container
docker ps
curl -I http://localhost:8080/

# Check the backend responds
curl http://localhost:8090/api/stats
```
