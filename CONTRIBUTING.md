# Contributing to S.W.A.R.M.

## How to report a bug

Open an issue with:
- What happened
- Steps to reproduce
- Expected vs actual behavior
- OS, Go version

## How to submit code

1. Fork the repository
2. Create a branch: `git checkout -b fix/issue-123`
3. Make changes
4. Ensure `go build ./...` and `go vet ./...` pass
5. Submit pull request

## Code style

- Standard Go formatting (`gofmt`)
- Comments in English
- No hardcoded IPs, passwords, or keys in commits

## Security

Do NOT open public issues for security vulnerabilities.
Report privately to maintainers.

## Areas needing help

- Windows / macOS client
- Android / iOS app
- Web UI improvements
- Security audit
- Translations
