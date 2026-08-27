# wrack

> THIS PROJECT WAS MADE PARTIALLY USING AGENTIC AI CODING TOOLS

A multi-token Discord nuke/raid CLI written in Go. It fans out deletion and
creation jobs across many tokens at once, rotates proxies, and backs off on
Discord rate limits instead of hammering through them.

> **For servers you own or are authorized to test.** Point this at a server you
> don't control and you're breaking Discord's Terms of Service. Accounts get
> banned. You're on your own.

## Status

Beta. Current release is **v0.2.0**. See [Versions](#versions).

## Features

- multi-token execution with a per-token permission audit
- proxy rotation (HTTP / SOCKS4 / SOCKS5) plus a direct fallback
- rate-limit aware: honors `Retry-After`, aborts on long global bans
- three modes: `nuke`, `raid`, `message-only`
- discohook JSON payloads, including components v2
- read-only recon snapshot taken before any writes

## Build

Requires Go 1.26+.

```bash
make build      # ./wrack for your platform
make release    # dist/ with all 6 targets (darwin/linux/windows × amd64/arm64)
```

## Usage

```bash
./wrack -guild <GUILD_ID> -tokens tokens.txt -mode nuke -y
```

| flag | meaning |
|------|---------|
| `-guild` | target guild ID (required) |
| `-tokens` | one token per line; bot or user tokens |
| `-mode`  | `nuke` / `raid` / `message-only` |
| `-y`     | skip the confirmation prompt |

Full flag list: `./wrack -h`.

## Versions

Releases follow `vMAJOR.MINOR.PATCH`. The number is set in `main.go` (`version`)
and the `Makefile` (`VERSION`), and is printed by `./wrack -version`.

It bumps on every semi-large or large change. To cut a release:

```bash
git tag v0.3.0
git push origin v0.3.0
```

Pushing a `v*` tag builds every target and publishes them to GitHub Releases.

## Disclaimer

Educational and authorized-testing use only. The author takes no responsibility
for misuse, bans, or damage. You are responsible for following Discord's ToS and
the law.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).
