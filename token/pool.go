// Package token classifies tokens, audits them, and shards work across them.
package token

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/M1noa/wrack/api"
)

// Token is one credential with its audit results.
type Token struct {
	Raw      string
	Client   *api.Client
	Kind     string // "bot" | "user"
	UserID   string
	Username string
	InGuild  bool
	Perms    int64
	Roles    []string
	IsOwner  bool
	Errors   []string
}

// HasPerm reports whether this token has the given permission bit(s).
func (t *Token) HasPerm(bit int64) bool {
	if t.Perms&api.PermAdministrator != 0 {
		return true
	}
	return t.Perms&bit != 0
}

// HasAllPerms checks multiple bits.
func (t *Token) HasAllPerms(bits ...int64) bool {
	for _, b := range bits {
		if !t.HasPerm(b) {
			return false
		}
	}
	return true
}

// Missing lists the named perms this token lacks from a required set.
func (t *Token) Missing(required map[string]int64) []string {
	var missing []string
	for name, bit := range required {
		if !t.HasPerm(bit) {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

// RequiredPerms is what a fully-capable nuke token should have.
var RequiredPerms = map[string]int64{
	"MANAGE_GUILD":               api.PermManageGuild,
	"MANAGE_CHANNELS":            api.PermManageChannels,
	"MANAGE_ROLES":               api.PermManageRoles,
	"BAN_MEMBERS":                api.PermBanMembers,
	"KICK_MEMBERS":               api.PermKickMembers,
	"MANAGE_MESSAGES":            api.PermManageMessages,
	"MANAGE_WEBHOOKS":            api.PermManageWebhooks,
	"MANAGE_EMOJIS_AND_STICKERS": api.PermManageEmojisAndStickers,
}

// Audit validates one token against the guild: identity, membership, perms.
func Audit(ctx context.Context, raw, guildID string, newClient func(string) *api.Client) (*Token, error) {
	t := &Token{Raw: strings.TrimSpace(raw)}
	c := newClient(t.Raw)

	// 1. Identity (also classifies bot vs user on the client).
	u, err := c.Me(ctx)
	if err != nil {
		return t, fmt.Errorf("auth failed: %w", err)
	}
	t.Client = c
	if u.Bot {
		t.Kind = "bot"
	} else {
		t.Kind = "user"
	}
	t.UserID = u.ID
	t.Username = u.Username

	// 2. Membership. Bot: /guilds/:id/members/@me. User: /users/@me/guilds scan.
	if u.Bot {
		m, err := c.MyMembership(ctx, guildID)
		if err != nil {
			t.Errors = append(t.Errors, fmt.Sprintf("bot not in guild %s (%v)", guildID, err))
			return t, nil
		}
		t.InGuild = true
		t.Roles = m.Roles
	} else {
		guilds, err := c.MyGuilds(ctx)
		if err != nil {
			t.Errors = append(t.Errors, fmt.Sprintf("cannot list user's guilds: %v", err))
			return t, nil
		}
		found := false
		for _, g := range guilds {
			if g.ID == guildID {
				found = true
				break
			}
		}
		if !found {
			t.Errors = append(t.Errors, fmt.Sprintf("user account not in guild %s", guildID))
			return t, nil
		}
		t.InGuild = true
		// Fetch member object for roles.
		m, err := c.MyMembership(ctx, guildID)
		if err == nil {
			t.Roles = m.Roles
		}
	}
	if t.UserID == guildID || strings.Contains(strings.ToLower(t.Username), "owner") {
		// Heuristic only; real ownership check happens via guild.owner_id compare in caller.
	}

	// 3. Perms: sum of role bitfields. Caller must supply role list; we compute
	// lazily in ComputePerms when the caller passes guild roles.
	return t, nil
}

// ComputePerms fills in the token's effective permissions given the guild's
// role list and owner ID. The @everyone role (id == guild id) is the baseline
// even when Discord omits it from member.roles.
func (t *Token) ComputePerms(guildRoles []api.Role, ownerID, guildID string) {
	if t.UserID == ownerID {
		t.Perms = ^int64(0) // owner = all
		t.IsOwner = true
		return
	}
	roleByID := make(map[string]api.Role, len(guildRoles))
	for _, r := range guildRoles {
		roleByID[r.ID] = r
	}
	var perms int64
	// Baseline: @everyone applies to every member.
	if ev, ok := roleByID[guildID]; ok {
		perms |= parseBits(ev.Perms)
	}
	for _, rid := range t.Roles {
		r, ok := roleByID[rid]
		if !ok {
			continue
		}
		p := parseBits(r.Perms)
		perms |= p
		if p&api.PermAdministrator != 0 {
			t.Perms = ^int64(0)
			return
		}
	}
	t.Perms = perms
}

func parseBits(s string) int64 {
	var v int64
	for _, ch := range s {
		v *= 10
		if ch >= '0' && ch <= '9' {
			v += int64(ch - '0')
		}
	}
	return v
}

// Pool holds audited tokens ready for work assignment.
type Pool struct {
	Tokens []*Token
	mu     sync.Mutex
	idx    int
}

// NewPool wraps audited tokens.
func NewPool(tokens []*Token) *Pool { return &Pool{Tokens: tokens} }

// Next returns the next usable token round-robin.
func (p *Pool) Next() *Token {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := len(p.Tokens)
	if n == 0 {
		return nil
	}
	for i := 0; i < n; i++ {
		t := p.Tokens[(p.idx+i)%n]
		if t.InGuild && len(t.Errors) == 0 {
			p.idx = (p.idx + i + 1) % n
			return t
		}
	}
	return nil
}

// WithPerm returns tokens that have the given permission bits.
func (p *Pool) WithPerm(bits ...int64) []*Token {
	var out []*Token
	for _, t := range p.Tokens {
		if t.InGuild && t.HasAllPerms(bits...) {
			out = append(out, t)
		}
	}
	return out
}

// Shard splits items across n workers as evenly as possible.
func Shard[T any](items []T, n int) [][]T {
	if n <= 0 {
		n = 1
	}
	out := make([][]T, n)
	for i, it := range items {
		out[i%n] = append(out[i%n], it)
	}
	return out
}
