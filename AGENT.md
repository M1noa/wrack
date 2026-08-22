# AGENT.md

Discord nuke/raid CLI in Go. Multi-token, proxy-rotated, rate-limit-aware.

## Setup
```
go mod download
make build          # ./wrack
make release        # dist/ with all 6 platform targets
./wrack -h
```

## Architecture
- `api/` — HTTP client + rate-limit transport + endpoints. Each Client owns its transport/buckets/token.
- `proxy/` — embedded sources.json → parallel scrape → Discord CDN test (<80ms) → round-robin Pool. SOCKS4 is hand-rolled in socks4.go (x/net lacks it).
- `token/` — audit (identity/membership/perms), ComputePerms from role bitfields, Shard() for work distribution.
- `recon/` — read-only guild snapshot, all lists in parallel.
- `nuke/` — deletion order matters: bulk-ban → automod → invites → channels → emojis/stickers/sounds → roles → strip settings.
- `raid/` — creation spam with caps from flags.
- `payload/` — discohook JSON → Message; components v2 auto-flagged with IS_COMPONENTS_V2=32768.
- `ui/` — random figlet font + HSL gradient banner; AccentColor shared with raid payloads.

## Gotchas
- NEVER ignore 429 Retry-After. Hammering through small limits escalates to ~45min GLOBAL token bans (empirically verified: retry-after: 2616). Smart-hammer: wait exactly the stated reset (<=30s), abort on >30s punishments.
- Go methods can't have type params — generic helpers must be free functions taking *Engine.
- fanOut is a free generic function in nuke/, not a method; same pattern in raid/.
- Server tag field on PATCH /guilds 400s on most guilds — always retry without it.
- Sticker create is multipart-only; JSON path returns errNotImpl (see raid.go TODO).
- recon.Take uses one probe token; if that token's perms are narrow, some lists come back empty (surfaced as warnings).
