// Package recon performs a read-only snapshot of a guild's full state so the
// nuke can fire instantly without discovery round-trips.
package recon

import (
	"context"
	"sync"

	"github.com/M1noa/wrack/api"
)

// Snapshot is the full read-only state of a guild at a point in time.
type Snapshot struct {
	Guild      *api.Guild
	Channels   []api.Channel
	Roles      []api.Role
	Emojis     []api.Emoji
	Stickers   []api.Sticker
	Sounds     []api.SoundboardSound
	Invites    []api.Invite
	AutoMod    []api.AutoModRule
	Webhooks   []api.Webhook
	Members    []api.Member
	Categories []api.Channel
	TextVoice  []api.Channel
}

// Take captures everything in parallel using whichever token is passed.
// Any single failure doesn't abort — we just leave that slice empty and
// report it in Warnings.
func Take(ctx context.Context, c *api.Client, guildID string, includeMembers bool) (*Snapshot, []string) {
	s := &Snapshot{}
	var warnings []string
	var wg sync.WaitGroup
	var mu sync.Mutex

	run := func(name string, fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(); err != nil {
				mu.Lock()
				warnings = append(warnings, name+": "+err.Error())
				mu.Unlock()
			}
		}()
	}

	run("guild", func() error {
		g, err := c.GetGuild(ctx, guildID, true)
		if err != nil {
			return err
		}
		s.Guild = g
		return nil
	})
	run("channels", func() error {
		cs, err := c.ListChannels(ctx, guildID)
		if err != nil {
			return err
		}
		for _, ch := range cs {
			switch ch.Type {
			case 4:
				s.Categories = append(s.Categories, ch)
			default:
				s.TextVoice = append(s.TextVoice, ch)
			}
		}
		s.Channels = cs
		return nil
	})
	run("roles", func() error {
		rs, err := c.ListRoles(ctx, guildID)
		if err != nil {
			return err
		}
		s.Roles = rs
		return nil
	})
	run("emojis", func() error {
		es, err := c.ListEmojis(ctx, guildID)
		if err != nil {
			return err
		}
		s.Emojis = es
		return nil
	})
	run("stickers", func() error {
		ss, err := c.ListStickers(ctx, guildID)
		if err != nil {
			return err
		}
		s.Stickers = ss
		return nil
	})
	run("sounds", func() error {
		ss, err := c.ListSounds(ctx, guildID)
		if err != nil {
			return err
		}
		s.Sounds = ss
		return nil
	})
	run("invites", func() error {
		is, err := c.ListInvites(ctx, guildID)
		if err != nil {
			return err
		}
		s.Invites = is
		return nil
	})
	run("automod", func() error {
		rs, err := c.ListAutoModRules(ctx, guildID)
		if err != nil {
			return err
		}
		s.AutoMod = rs
		return nil
	})
	run("webhooks", func() error {
		ws, err := c.ListWebhooks(ctx, guildID)
		if err != nil {
			return err
		}
		s.Webhooks = ws
		return nil
	})

	if includeMembers {
		run("members", func() error {
			ms, err := pageMembers(ctx, c, guildID)
			if err != nil {
				return err
			}
			s.Members = ms
			return nil
		})
	}

	wg.Wait()
	return s, warnings
}

func pageMembers(ctx context.Context, c *api.Client, guildID string) ([]api.Member, error) {
	out := make([]api.Member, 0, 1000)
	after := ""
	for {
		batch, err := c.ListMembers(ctx, guildID, after, 1000)
		if err != nil {
			return out, err
		}
		if len(batch) == 0 {
			break
		}
		out = append(out, batch...)
		after = batch[len(batch)-1].User.ID
		if len(batch) < 1000 {
			break
		}
	}
	return out, nil
}
