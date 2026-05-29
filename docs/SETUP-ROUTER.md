# Setup: Router Path (Path 1)

Route your **entire home network** through S.W.A.R.M. in 3 steps. No app installation needed on phones, TVs, or game consoles — everything on your LAN is protected automatically.

```
All LAN devices (phones, TVs, PCs, consoles)
          ↓  (gateway 192.168.x.1)
    Home server / router
      swarm-node (client mode)
      TPROXY :12346  ←── all LAN traffic
      SOCKS5 :1090
          ↓  QUIC + ChaCha20-Poly1305
    Bootstrap VDS (e.g. Stockholm)
          ↓
       Internet
```

## What you need

- A Linux machine that acts as your LAN gateway (home server, Raspberry Pi, old PC, OpenWrt router)
- At least one S.W.A.R.M. bootstrap node (run your own VDS or use the public network)
- Root / sudo access on the gateway machine

## Step 1 — Install swarm-node on the gateway

```bash
# Download the latest binary for Linux amd64
curl -Lo /usr/local/bin/swarm-node \
  https://github.com/Rockenrol2017/swarm/releases/latest/download/swarm-node-linux-amd64
chmod +x /usr/local/bin/swarm-node

# Grant TPROXY capability (required for transparent proxy, no root needed at runtime)
sudo setcap cap_net_admin=+ep /usr/local/bin/swarm-node
```

> **Build from source:**
> ```bash
> git clone https://github.com/Rockenrol2017/swarm
> cd swarm/src
> go build -o /usr/local/bin/swarm-node ./cmd/swarm-node/
> sudo setcap cap_net_admin=+ep /usr/local/bin/swarm-node
> ```

## Step 2 — Configure as client node

Create `/etc/swarm/node-config.json`:

```json
{
  "mode": "client",
  "bootstrap_addrs": ["YOUR_VDS_IP:7437"],
  "socks5_addr": ":1090",
  "tproxy_addr": ":12346",
  "identity_file": "/etc/swarm/identity.json",
  "max_peers": 10,
  "status_addr": ":19090",
  "traffic_file": "/var/lib/swarm/traffic.json",
  "max_relay_percent": 20
}
```

Replace `YOUR_VDS_IP` with your bootstrap server IP. For the public S.W.A.R.M. network:

```json
"bootstrap_addrs": [
  "193.68.89.168:7437"
]
```

## Step 3 — Enable TPROXY routing

TPROXY intercepts all outgoing TCP/UDP traffic transparently — devices don't need any proxy settings.

```bash
# Download and run the TPROXY setup script
sudo bash install/tproxy-rules.sh

# Or manually:
# Create routing table 101
echo "101 swarm" | sudo tee -a /etc/iproute2/rt_tables

# Mark packets for swarm routing
sudo iptables -t mangle -N SWARM_OUT
sudo iptables -t mangle -A SWARM_OUT -d 10.0.0.0/8 -j RETURN
sudo iptables -t mangle -A SWARM_OUT -d 172.16.0.0/12 -j RETURN
sudo iptables -t mangle -A SWARM_OUT -d 192.168.0.0/16 -j RETURN
sudo iptables -t mangle -A SWARM_OUT -p tcp -j MARK --set-mark 0x2
sudo iptables -t mangle -A PREROUTING -j SWARM_OUT

# Route marked traffic to TPROXY
sudo ip rule add fwmark 0x2 table 101
sudo ip route add local 0.0.0.0/0 dev lo table 101

# TPROXY rule
sudo iptables -t mangle -A PREROUTING \
  -p tcp -m mark --mark 0x2 \
  -j TPROXY --tproxy-mark 0x2 --on-port 12346
```

Make persistent: `sudo iptables-save > /etc/iptables/rules.v4`

## Run as a service

```bash
sudo cp install/systemd/swarm-node.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now swarm-node
```

Check it's working:

```bash
# Status
systemctl status swarm-node

# Verify traffic is going through swarm
curl https://api.ipinfo.io/ip
# Should show your VDS IP, not your ISP IP

# Dashboard
curl -s http://localhost:19090/api/status | python3 -m json.tool
```

## Verify

```bash
# From any device on the LAN (no proxy settings needed):
curl https://api.ipinfo.io/ip   # shows VDS IP

# Check swarm status from gateway:
curl -s http://GATEWAY_IP:19090/api/status
```

You should see:
```json
{
  "mode": "client",
  "peers": 1,
  "latency_ms": 45,
  "channel_rx_mbps": 2.3
}
```

## Troubleshooting

| Problem | Solution |
|---------|----------|
| `peers: 0` | Check bootstrap IP and UDP 7437 is open on VDS |
| TPROXY not working | Verify `cap_net_admin` is set: `getcap /usr/local/bin/swarm-node` |
| DNS not resolving | Add `--on-port 12346` for UDP too, or use separate DNS rule |
| High latency | Normal for satellite: 1400-1900ms. Check `latency_ms` in /api/status |
| Traffic not routing | Run `install/tproxy-rules.sh` again after reboot |

## See also

- [Setup: App Path](SETUP-APP.md) — install the app on individual devices
- [Architecture](ARCHITECTURE.md) — how S.W.A.R.M. works under the hood
- [Bootstrap deploy](../install/deploy-bootstrap.sh) — run your own VDS node
