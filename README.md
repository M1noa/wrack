# wrack

Experimental Discord nuke/raid CLI — multi-token, proxy-rotated, rate-limit-aware. Stress-tests Discord API limits and concurrency patterns. **For experimentation on your own servers only.**

## Build

```bash
make build          # local binary
make release        # all 6 targets into dist/ (darwin/linux/windows × amd64/arm64)
```

## Usage

```bash
./wrack -guild <ID> -tokens tokens.txt [-mode nuke|raid|message-only] [flags]
```

Tokens: one per line in `tokens.txt`. Bot tokens and user tokens both work (user tokens are ToS-risky — your call).

## Flags

Run `./wrack -h` for the full list. Highlights:

- `-mode` — `nuke` (wipe), `raid` (wipe + spam-create), `message-only`
- `-message FILE` — discohook JSON export (Options → JSON Editor → Download → Plain JSON); supports components v2
- `-short MSG` — name used for channels/roles/webhooks/server tag/bio/rules
- `-image FILE` — image for server pfp + emojis + stickers
- `-proxy-file FILE -proxy-type http|socks4|socks5` — custom proxies; otherwise scrapes from embedded list
- `-no-proxy`, `-no-proxy-test`, `-proxy-ms N` — proxy controls
- `-y` / `-yes` — skip confirmation
- `-ignore-errors` — run even if some tokens fail audit
- `-max-channels/-roles/-emojis/-stickers/-sounds N` — caps on spam creation

## Architecture

- `main.go` — flags, orchestration, confirm flow
- `api/` — HTTP client, rate-limit transport, typed endpoint wrappers
- `proxy/` — scrape (embedded sources.json) → test against Discord CDN → pool; SOCKS4 hand-rolled, SOCKS5 via x/net
- `token/` — classify (bot/user), audit membership + perms, shard work
- `recon/` — read-only snapshot of everything before any writes
- `perms/` — per-token audit report
- `nuke/` — ordered deletion engine (bulk-ban first, channels cascade webhooks)
- `raid/` — creation spam engine (channels/roles/emojis/stickers/sounds/webhooks/messages)
- `payload/` — discohook parse, components v2 flagging, blank PNG/silent MP3 generation
- `ui/` — random-font banner, gradient, prompts, progress counters

## Gotchas

- Bulk ban (`POST /guilds/:id/bulk-ban`) = 200 users/request, needs BAN_MEMBERS + MANAGE_GUILD.
- Channel deletion cascades its webhooks + messages — don't double-delete.
- Server tag isn't writable via standard PATCH /guilds; we send it anyway and ignore 400s.
- Sticker creation needs multipart upload (TODO — not wired).
- Roles: can't delete @everyone or managed roles.
