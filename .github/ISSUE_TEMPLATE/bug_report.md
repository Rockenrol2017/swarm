---
name: Bug report
about: Something is not working
title: '[BUG] '
labels: bug
assignees: ''
---

**Describe the bug**
A clear description of what the bug is.

**Node info**
- Mode: `client` / `relay` / `bootstrap`
- OS: (e.g. Ubuntu 24.04, Debian 12)
- Arch: (amd64 / arm64 / armv7)
- Version: (run `swarm-node -version` or check release tag)

**Config** (remove any private IPs/keys)
```json
{
  "mode": "...",
  "socks5_addr": ":1090"
}
```

**Logs**
```
journalctl -u swarm-node -n 50 --no-pager
```

**Expected behavior**
What you expected to happen.

**Additional context**
Network type (satellite / cable / mobile), RTT to bootstrap, etc.
