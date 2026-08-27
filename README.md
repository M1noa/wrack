# wrack

> THIS PROJECT WAS MADE PARTIALLY USING AGENTIC AI CODING TOOLS

A multi-token Discord nuke/raid CLI written in Go. It fans out deletion and
creation jobs across many tokens at once, rotates proxies, and backs off on
Discord rate limits instead of hammering through them.

---

> **For servers you own or are authorized to test.** Point this at a server you
> don't control and you're breaking Discord's Terms of Service.

## Features

- multi-token usage
- proxy rotation (HTTP / SOCKS4 / SOCKS5)
- rate-limit aware; honors `Retry-After`, aborts on long global bans
- three modes: `nuke`, `raid`, `message-only`
- embed support with components v2 via discohook json backups/exports

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

Full flag list: `./wrack -h`.

## Disclaimer

Educational and authorized-testing use only. The author takes no responsibility
for misuse, bans, or damage. You are responsible for following Discord's ToS and
the law.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).
