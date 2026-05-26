# Example configs

| File | Mode | Use case |
|------|------|----------|
| `bootstrap.json` | bootstrap | VPS entry point — accepts connections, proxies to internet |
| `relay.json` | relay | Intermediate hop: Client → Relay → Bootstrap → Internet |
| `client.json` | client | Home server / single bootstrap |
| `client-multi-bootstrap.json` | client | Home server / multiple bootstraps for redundancy |

## Quick setup

Copy the relevant file to `/etc/swarm/node-config.json` and replace placeholder IPs:

```bash
cp configs/bootstrap.json /etc/swarm/node-config.json
# edit: nano /etc/swarm/node-config.json
```

Or use the one-liner installer which sets everything up automatically:

```bash
curl -sSL https://raw.githubusercontent.com/Rockenrol2017/swarm/main/install.sh | bash
```
