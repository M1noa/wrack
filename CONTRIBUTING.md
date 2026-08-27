# Contributing

Thanks for poking at `wrack`. A few ground rules before you open a PR.

## What belongs here

- bug fixes for the deletion / creation engines
- proxy transport improvements (HTTP / SOCKS4 / SOCKS5)
- rate-limit handling that respects `Retry-After`
- payload parsing (discohook JSON, components v2)

Keep changes focused. One PR does one thing.

## What doesn't

- anything that ignores or works around Discord rate limits on purpose
- features aimed at servers you don't own or have permission to test
- token harvesting or account theft

## Before you open a PR

1. `go build ./...` and `go test ./...` pass locally
2. run `gofmt -w .` on touched files
3. update `main.go` `version` and the `Makefile` `VERSION` only when cutting a
   release, not on feature PRs
4. add a one-line note to `CHANGELOG.md` under the unreleased section

## Commit style

Short imperative subject, under 70 chars. Body only if the change needs
context.

## Releasing

Tag a `vMAJOR.MINOR.PATCH` and push it. The release workflow builds all
targets and publishes them to GitHub Releases. Bump the version on every
semi-large or large change.
