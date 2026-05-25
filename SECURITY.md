# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| Latest  | ✅ |

## Reporting a Vulnerability

Do NOT report security vulnerabilities through public GitHub issues.

Describe the issue privately to maintainers:
- Type of vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (optional)

We will respond within 72 hours.

## Security Design

- ChaCha20-Poly1305 authenticated encryption
- X25519 ephemeral key exchange per session
- Ed25519 node identity signatures
- Zero logs — no traffic content logged
- Open source — fully auditable
