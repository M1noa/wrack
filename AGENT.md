# thnuke

Experimental Discord "nuke" bot — wipes channels/roles/emojis/messages as fast as the API allows. Built to stress-test Discord rate limits and concurrency patterns. **For experimentation on your own servers only.**

## Setup

```bash
go build -o thnuke .
./thnuke -token <BOT_TOKEN> -guild <GUILD_ID>
```

## Architecture

- `main.go` — entry, flag parsing, orchestration
- `wipe.go` — concurrent channel/role/emoji deletion
- `purge.go` — bulk message deletion (channel-by-channel)

## Gotchas

- Discord rate limits: use `X-RateLimit-Bucket` headers + 429 handling, not fixed delays.
- `DELETE /channels/:id/messages/:id` is 2/s per channel; bulk-delete (50 msgs, 14-day window) is faster but has its own bucket.
- Roles: can't delete `@everyone` or managed roles — skip or you'll get 403s and waste requests.
- Emojis: 50 per guild max, single endpoint, easy win.
