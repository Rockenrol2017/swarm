# Setup: App Path (Path 2)

Install the S.W.A.R.M. client directly on your device. Best for laptops, phones, and single devices — no router configuration needed.

```
Your device
  S.W.A.R.M. app
  SOCKS5 :1090  ←── browser / apps (manual proxy settings)
  VPN mode      ←── routes all traffic (Android, planned)
      ↓  QUIC + ChaCha20-Poly1305
  Bootstrap VDS
      ↓
   Internet
```

## Platform support

| Platform | Status | Method |
|----------|--------|--------|
| Linux | ✅ Ready | Binary / systemd |
| macOS | ✅ Works | Binary (SOCKS5 proxy) |
| Windows | ✅ Works | Binary (SOCKS5 proxy) |
| Android | 🔧 In development | App (VpnService) |
| iOS | 📋 Planned | App (NetworkExtension) |
| OpenWrt | ✅ Works | Binary (MIPS/ARM build) |

> **Router path is easier** for whole-home coverage. See [SETUP-ROUTER.md](SETUP-ROUTER.md).

---

## Linux

### Install

```bash
curl -Lo /usr/local/bin/swarm-node \
  https://github.com/Rockenrol2017/swarm/releases/latest/download/swarm-node-linux-amd64
chmod +x /usr/local/bin/swarm-node
```

### Configure

`/etc/swarm/node-config.json`:
```json
{
  "mode": "client",
  "bootstrap_addrs": ["YOUR_VDS_IP:7437"],
  "socks5_addr": ":1090",
  "identity_file": "/etc/swarm/identity.json",
  "status_addr": ":19090",
  "max_relay_percent": 20
}
```

### Run

```bash
swarm-node -config /etc/swarm/node-config.json
# or as a service:
sudo systemctl enable --now swarm-node
```

### Configure browser proxy

Firefox: Settings → Network Settings → Manual proxy → SOCKS5 Host: `127.0.0.1`, Port: `1090`

Chrome: launch with `--proxy-server="socks5://127.0.0.1:1090"`

System-wide (Linux): `export ALL_PROXY=socks5://127.0.0.1:1090`

---

## macOS

### Install

```bash
curl -Lo /usr/local/bin/swarm-node \
  https://github.com/Rockenrol2017/swarm/releases/latest/download/swarm-node-darwin-amd64
chmod +x /usr/local/bin/swarm-node
```

Apple Silicon:
```bash
curl -Lo /usr/local/bin/swarm-node \
  https://github.com/Rockenrol2017/swarm/releases/latest/download/swarm-node-darwin-arm64
```

### Configure and run

Same config as Linux. Run in terminal:

```bash
swarm-node -config ~/swarm-config.json
```

### System proxy

System Preferences → Network → Advanced → Proxies → SOCKS Proxy:
- Server: `127.0.0.1`
- Port: `1090`

---

## Windows

### Install

Download from [Releases](https://github.com/Rockenrol2017/swarm/releases):
`swarm-node-windows-amd64.exe`

Create config at `C:\swarm\node-config.json`:
```json
{
  "mode": "client",
  "bootstrap_addrs": ["YOUR_VDS_IP:7437"],
  "socks5_addr": ":1090",
  "identity_file": "C:\\swarm\\identity.json",
  "status_addr": ":19090",
  "max_relay_percent": 20
}
```

Run in PowerShell:
```powershell
.\swarm-node.exe -config C:\swarm\node-config.json
```

### System proxy

Settings → Network & Internet → Proxy → Manual proxy setup:
- Use a proxy server: On
- Address: `127.0.0.1`, Port: `1090`

---

## Android (in development)

The Android app is under development. When released, it will:

1. Install from F-Droid or GitHub releases (APK)
2. Open app → tap **Connect**
3. Accept VPN permission prompt
4. All traffic routed through swarm automatically

**Current workaround:** use S.W.A.R.M. via your home router running the client node (see [Router Path](SETUP-ROUTER.md)), or configure your Android WiFi to use SOCKS5 proxy manually:

WiFi Settings → (long press network) → Modify → Advanced → Proxy: Manual
- Hostname: `YOUR_GATEWAY_IP`
- Port: `1090`

See [Android development docs](../android/README.md) for more information.

---

## Verify it's working

From any platform after starting swarm-node:

```bash
# Should show your VDS IP, not your real IP
curl --proxy socks5://127.0.0.1:1090 https://api.ipinfo.io/ip

# Node status
curl http://127.0.0.1:19090/api/status
```

Expected:
```json
{
  "mode": "client",
  "peers": 1,
  "latency_ms": 45
}
```

---

## Troubleshooting

| Problem | Solution |
|---------|----------|
| `peers: 0` | Check bootstrap IP is correct and reachable (UDP 7437) |
| Proxy not working | Verify swarm-node is running: `curl http://127.0.0.1:19090/health` |
| Slow speed | Normal for encrypted mesh. Try a geographically closer bootstrap node |
| App won't start | Check config file path and JSON syntax |

---

## See also

- [Setup: Router Path](SETUP-ROUTER.md) — whole-home coverage, no per-device setup
- [Android](../android/README.md) — native Android app (in development)
- [Architecture](ARCHITECTURE.md) — technical details
- [Run a bootstrap node](../README.md#run-a-node) — contribute to the network
