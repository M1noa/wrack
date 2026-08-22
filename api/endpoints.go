package api

import (
	"context"
	"fmt"
	"time"
)

// ---- shared types (subset of Discord API objects we touch) ----

type User struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	Discriminator string `json:"discriminator"`
	Bot           bool   `json:"bot"`
}

type Guild struct {
	ID                          string   `json:"id"`
	Name                        string   `json:"name"`
	Description                 *string  `json:"description"`
	VerificationLevel           int      `json:"verification_level"`
	DefaultMessageNotifications int      `json:"default_message_notifications"`
	ExplicitContentFilter       int      `json:"explicit_content_filter"`
	NSFW                        bool     `json:"nsfw"`
	NSFWLevel                   int      `json:"nsfw_level"`
	PremiumTier                 int      `json:"premium_tier"`
	PremiumSubscriptionCount    int      `json:"premium_subscription_count"`
	PreferredLocale             string   `json:"preferred_locale"`
	RulesChannelID              *string  `json:"rules_channel_id"`
	PublicUpdatesChannelID      *string  `json:"public_updates_channel_id"`
	SystemChannelID             *string  `json:"system_channel_id"`
	AfkChannelID                *string  `json:"afk_channel_id"`
	AfkTimeout                  int      `json:"afk_timeout"`
	OwnerID                     string   `json:"owner_id"`
	Icon                        *string  `json:"icon"`
	Banner                      *string  `json:"banner"`
	Splash                      *string  `json:"splash"`
	Features                    []string `json:"features"`
	VanityURLCode               *string  `json:"vanity_url_code"`
	ApproxMemberCount           int      `json:"approximate_member_count"`
	ApproxPresenceCount         int      `json:"approximate_presence_count"`
}

type Channel struct {
	ID        string  `json:"id"`
	GuildID   string  `json:"guild_id"`
	Name      string  `json:"name"`
	Type      int     `json:"type"` // 0=text,2=voice,4=category,5=announce,13=stage,15=forum,16=media
	ParentID  *string `json:"parent_id"`
	NSFW      bool    `json:"nsfw"`
	Topic     *string `json:"topic"`
	Position  int     `json:"position"`
}

type Role struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Color     int    `json:"color"`
	Hoist     bool   `json:"hoist"`
	Position  int    `json:"position"`
	Perms     string `json:"permissions"` // bitfield as string
	Managed   bool   `json:"managed"`
	Mentionable bool `json:"mentionable"`
}

type Emoji struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Animated bool   `json:"animated"`
	Managed  bool   `json:"managed"`
}

type Sticker struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description *string `json:"description"`
	Tags        string `json:"tags"`
	FormatType  int    `json:"format_type"`
	Available   bool   `json:"available"`
}

type SoundboardSound struct {
	Name    string  `json:"name"`
	SoundID string  `json:"sound_id"`
	Volume  float64 `json:"volume"`
	EmojiID *string `json:"emoji_id"`
	EmojiName *string `json:"emoji_name"`
	Available bool  `json:"available"`
	GuildID string  `json:"guild_id"`
}

type Invite struct {
	Code      string `json:"code"`
	GuildID   string `json:"guild_id"`
	ChannelID string `json:"channel_id"`
	Inviter   *User  `json:"inviter"`
	Uses      int    `json:"uses"`
	MaxUses   int    `json:"max_uses"`
}

type AutoModRule struct {
	ID         string        `json:"id"`
	GuildID    string        `json:"guild_id"`
	Name       string        `json:"name"`
	CreatorID  string        `json:"creator_id"`
	EventType  int           `json:"event_type"`
	TriggerType int          `json:"trigger_type"`
	Enabled    bool          `json:"enabled"`
}

type Webhook struct {
	ID       string  `json:"id"`
	Type     int     `json:"type"`
	GuildID  string  `json:"guild_id"`
	ChannelID string `json:"channel_id"`
	Name     string  `json:"name"`
	Token    *string `json:"token"`
	URL      *string `json:"url"`
}

type Member struct {
	User     *User    `json:"user"`
	Nick     *string  `json:"nick"`
	Roles    []string `json:"roles"`
	JoinedAt string   `json:"joined_at"`
	Pending  bool     `json:"pending"`
}

// ---- endpoints ----

// Me fetches the current user identity. Classifies the token on the way:
// tries "Bot <token>" first, falls back to bare (user) token on 401.
func (c *Client) Me(ctx context.Context) (*User, error) {
	u, err := c.meOnce(ctx)
	if err != nil {
		return nil, err
	}
	c.UserID = u.ID
	if u.Bot {
		c.Kind = "bot"
	} else {
		c.Kind = "user"
	}
	return u, nil
}

func (c *Client) meOnce(ctx context.Context) (*User, error) {
	// Attempt order: bot-prefixed first, then bare.
	for _, kind := range []string{"bot", "user"} {
		c.Kind = kind
		resp, err := c.Do(ctx, "GET", "/users/@me", nil)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == 401 {
			DiscardBody(resp)
			continue
		}
		var u User
		if err := DecodeJSON(resp, &u); err != nil {
			return nil, err
		}
		return &u, nil
	}
	return nil, fmt.Errorf("invalid token")
}

// MyGuilds returns guilds visible to a user token.
func (c *Client) MyGuilds(ctx context.Context) ([]Guild, error) {
	resp, err := c.Do(ctx, "GET", "/users/@me/guilds", nil)
	if err != nil {
		return nil, err
	}
	var g []Guild
	if err := DecodeJSON(resp, &g); err != nil {
		return nil, err
	}
	return g, nil
}

// GetGuild fetches guild info. with_counts adds approximate member count.
func (c *Client) GetGuild(ctx context.Context, guildID string, withCounts bool) (*Guild, error) {
	opts := []ReqOpt{}
	if withCounts {
		opts = append(opts, WithQuery(map[string]string{"with_counts": "true"}))
	}
	resp, err := c.Do(ctx, "GET", Path("/guilds/%s", guildID), nil, opts...)
	if err != nil {
		return nil, err
	}
	var g Guild
	if err := DecodeJSON(resp, &g); err != nil {
		return nil, err
	}
	return &g, nil
}

// MyMembership returns this token's member object in the given guild.
// Uses the resolved user ID — /members/@me 400s on bot tokens.
func (c *Client) MyMembership(ctx context.Context, guildID string) (*Member, error) {
	if c.UserID == "" {
		if _, err := c.Me(ctx); err != nil {
			return nil, err
		}
	}
	resp, err := c.Do(ctx, "GET", Path("/guilds/%s/members/%s", guildID, c.UserID), nil)
	if err != nil {
		return nil, err
	}
	var m Member
	if err := DecodeJSON(resp, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// GetMember fetches any member by ID.
func (c *Client) GetMember(ctx context.Context, guildID, userID string) (*Member, error) {
	resp, err := c.Do(ctx, "GET", Path("/guilds/%s/members/%s", guildID, userID), nil)
	if err != nil {
		return nil, err
	}
	var m Member
	if err := DecodeJSON(resp, &m); err != nil {
		return nil, err
	}
	return &m, nil
}

// ListChannels lists all channels in the guild.
func (c *Client) ListChannels(ctx context.Context, guildID string) ([]Channel, error) {
	resp, err := c.Do(ctx, "GET", Path("/guilds/%s/channels", guildID), nil)
	if err != nil {
		return nil, err
	}
	var cs []Channel
	if err := DecodeJSON(resp, &cs); err != nil {
		return nil, err
	}
	return cs, nil
}

// DeleteChannel deletes a channel by ID.
func (c *Client) DeleteChannel(ctx context.Context, channelID, reason string) error {
	resp, err := c.Do(ctx, "DELETE", Path("/channels/%s", channelID), nil, WithReason(reason))
	if err != nil {
		return err
	}
	return DiscardBody(resp)
}

// CreateChannel creates a new guild channel.
func (c *Client) CreateChannel(ctx context.Context, guildID string, body map[string]any) (*Channel, error) {
	resp, err := c.Do(ctx, "POST", Path("/guilds/%s/channels", guildID), body)
	if err != nil {
		return nil, err
	}
	var ch Channel
	if err := DecodeJSON(resp, &ch); err != nil {
		return nil, err
	}
	return &ch, nil
}

// ModifyChannel updates a channel (used to set nsfw).
func (c *Client) ModifyChannel(ctx context.Context, channelID string, body map[string]any) error {
	resp, err := c.Do(ctx, "PATCH", Path("/channels/%s", channelID), body)
	if err != nil {
		return err
	}
	return DiscardBody(resp)
}

// ListRoles lists all roles in the guild.
func (c *Client) ListRoles(ctx context.Context, guildID string) ([]Role, error) {
	resp, err := c.Do(ctx, "GET", Path("/guilds/%s/roles", guildID), nil)
	if err != nil {
		return nil, err
	}
	var rs []Role
	if err := DecodeJSON(resp, &rs); err != nil {
		return nil, err
	}
	return rs, nil
}

// DeleteRole deletes a role by ID.
func (c *Client) DeleteRole(ctx context.Context, guildID, roleID, reason string) error {
	resp, err := c.Do(ctx, "DELETE", Path("/guilds/%s/roles/%s", guildID, roleID), nil, WithReason(reason))
	if err != nil {
		return err
	}
	return DiscardBody(resp)
}

// CreateRole creates a new guild role.
func (c *Client) CreateRole(ctx context.Context, guildID string, body map[string]any) (*Role, error) {
	resp, err := c.Do(ctx, "POST", Path("/guilds/%s/roles", guildID), body)
	if err != nil {
		return nil, err
	}
	var r Role
	if err := DecodeJSON(resp, &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ListEmojis lists all emojis in the guild.
func (c *Client) ListEmojis(ctx context.Context, guildID string) ([]Emoji, error) {
	resp, err := c.Do(ctx, "GET", Path("/guilds/%s/emojis", guildID), nil)
	if err != nil {
		return nil, err
	}
	var es []Emoji
	if err := DecodeJSON(resp, &es); err != nil {
		return nil, err
	}
	return es, nil
}

// CreateEmoji creates a new emoji.
func (c *Client) CreateEmoji(ctx context.Context, guildID string, body map[string]any) (*Emoji, error) {
	resp, err := c.Do(ctx, "POST", Path("/guilds/%s/emojis", guildID), body)
	if err != nil {
		return nil, err
	}
	var e Emoji
	if err := DecodeJSON(resp, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// DeleteEmoji deletes an emoji by ID.
func (c *Client) DeleteEmoji(ctx context.Context, guildID, emojiID, reason string) error {
	resp, err := c.Do(ctx, "DELETE", Path("/guilds/%s/emojis/%s", guildID, emojiID), nil, WithReason(reason))
	if err != nil {
		return err
	}
	return DiscardBody(resp)
}

// ListStickers lists all stickers in the guild.
func (c *Client) ListStickers(ctx context.Context, guildID string) ([]Sticker, error) {
	resp, err := c.Do(ctx, "GET", Path("/guilds/%s/stickers", guildID), nil)
	if err != nil {
		return nil, err
	}
	var ss []Sticker
	if err := DecodeJSON(resp, &ss); err != nil {
		return nil, err
	}
	return ss, nil
}

// CreateSticker uploads a new sticker via multipart/form-data.
// file must be PNG/APNG (or Lottie JSON), <512KB, >=320x320.
func (c *Client) CreateSticker(ctx context.Context, guildID, name, description, tags, fileName, contentType string, file []byte) (*Sticker, error) {
	fields := map[string]string{"name": name, "tags": tags}
	if description != "" {
		fields["description"] = description
	}
	resp, err := c.Do(ctx, "POST", Path("/guilds/%s/stickers", guildID), nil,
		WithFields(fields),
		WithFile("file", fileName, contentType, file))
	if err != nil {
		return nil, err
	}
	var s Sticker
	if err := DecodeJSON(resp, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// DeleteSticker deletes a sticker by ID.
func (c *Client) DeleteSticker(ctx context.Context, guildID, stickerID, reason string) error {
	resp, err := c.Do(ctx, "DELETE", Path("/guilds/%s/stickers/%s", guildID, stickerID), nil, WithReason(reason))
	if err != nil {
		return err
	}
	return DiscardBody(resp)
}

// ListSounds lists soundboard sounds for the guild. The endpoint returns
// {"items": [...]} rather than a bare array.
func (c *Client) ListSounds(ctx context.Context, guildID string) ([]SoundboardSound, error) {
	resp, err := c.Do(ctx, "GET", Path("/guilds/%s/soundboard-sounds", guildID), nil)
	if err != nil {
		return nil, err
	}
	var wrapped struct {
		Items []SoundboardSound `json:"items"`
	}
	if err := DecodeJSON(resp, &wrapped); err != nil {
		return nil, err
	}
	return wrapped.Items, nil
}

// DeleteSound deletes a soundboard sound by ID.
func (c *Client) DeleteSound(ctx context.Context, guildID, soundID, reason string) error {
	resp, err := c.Do(ctx, "DELETE", Path("/guilds/%s/soundboard-sounds/%s", guildID, soundID), nil, WithReason(reason))
	if err != nil {
		return err
	}
	return DiscardBody(resp)
}

// ListInvites lists all invites in the guild (requires MANAGE_GUILD).
func (c *Client) ListInvites(ctx context.Context, guildID string) ([]Invite, error) {
	resp, err := c.Do(ctx, "GET", Path("/guilds/%s/invites", guildID), nil)
	if err != nil {
		return nil, err
	}
	var is []Invite
	if err := DecodeJSON(resp, &is); err != nil {
		return nil, err
	}
	return is, nil
}

// DeleteInvite revokes an invite by code.
func (c *Client) DeleteInvite(ctx context.Context, code, reason string) error {
	resp, err := c.Do(ctx, "DELETE", Path("/invites/%s", code), nil, WithReason(reason))
	if err != nil {
		return err
	}
	return DiscardBody(resp)
}

// ListAutoModRules lists automod rules for the guild (requires MANAGE_GUILD).
func (c *Client) ListAutoModRules(ctx context.Context, guildID string) ([]AutoModRule, error) {
	resp, err := c.Do(ctx, "GET", Path("/guilds/%s/auto-moderation/rules", guildID), nil)
	if err != nil {
		return nil, err
	}
	var rs []AutoModRule
	if err := DecodeJSON(resp, &rs); err != nil {
		return nil, err
	}
	return rs, nil
}

// DeleteAutoModRule deletes an automod rule by ID.
func (c *Client) DeleteAutoModRule(ctx context.Context, guildID, ruleID, reason string) error {
	resp, err := c.Do(ctx, "DELETE", Path("/guilds/%s/auto-moderation/rules/%s", guildID, ruleID), nil, WithReason(reason))
	if err != nil {
		return err
	}
	return DiscardBody(resp)
}

// ListWebhooks lists webhooks for the whole guild.
func (c *Client) ListWebhooks(ctx context.Context, guildID string) ([]Webhook, error) {
	resp, err := c.Do(ctx, "GET", Path("/guilds/%s/webhooks", guildID), nil)
	if err != nil {
		return nil, err
	}
	var ws []Webhook
	if err := DecodeJSON(resp, &ws); err != nil {
		return nil, err
	}
	return ws, nil
}

// DeleteWebhook deletes a webhook by ID.
func (c *Client) DeleteWebhook(ctx context.Context, webhookID, reason string) error {
	resp, err := c.Do(ctx, "DELETE", Path("/webhooks/%s", webhookID), nil, WithReason(reason))
	if err != nil {
		return err
	}
	return DiscardBody(resp)
}

// BulkBan bans up to 200 users at once. Requires BAN_MEMBERS + MANAGE_GUILD.
func (c *Client) BulkBan(ctx context.Context, guildID string, userIDs []string, deleteSeconds int) (banned, failed []string, err error) {
	body := map[string]any{"user_ids": userIDs, "delete_message_seconds": deleteSeconds}
	resp, err := c.Do(ctx, "POST", Path("/guilds/%s/bulk-ban", guildID), body)
	if err != nil {
		return nil, nil, err
	}
	var out struct {
		BannedUsers []string `json:"banned_users"`
		FailedUsers []string `json:"failed_users"`
	}
	if err = DecodeJSON(resp, &out); err != nil {
		return nil, nil, err
	}
	return out.BannedUsers, out.FailedUsers, nil
}

// Ban bans one user with message deletion window.
func (c *Client) Ban(ctx context.Context, guildID, userID string, deleteSeconds int, reason string) error {
	body := map[string]any{"delete_message_seconds": deleteSeconds}
	resp, err := c.Do(ctx, "PUT", Path("/guilds/%s/bans/%s", guildID, userID), body, WithReason(reason))
	if err != nil {
		return err
	}
	return DiscardBody(resp)
}

// Kick removes a member without banning.
func (c *Client) Kick(ctx context.Context, guildID, userID, reason string) error {
	resp, err := c.Do(ctx, "DELETE", Path("/guilds/%s/members/%s", guildID, userID), nil, WithReason(reason))
	if err != nil {
		return err
	}
	return DiscardBody(resp)
}

// ListMembers pages through members (1000 per request).
func (c *Client) ListMembers(ctx context.Context, guildID string, after string, limit int) ([]Member, error) {
	q := map[string]string{"limit": fmt.Sprint(limit)}
	if after != "" {
		q["after"] = after
	}
	resp, err := c.Do(ctx, "GET", Path("/guilds/%s/members", guildID), nil, WithQuery(q))
	if err != nil {
		return nil, err
	}
	var ms []Member
	if err := DecodeJSON(resp, &ms); err != nil {
		return nil, err
	}
	return ms, nil
}

// ModifyGuild patches guild settings. Only fields present in the map are sent.
func (c *Client) ModifyGuild(ctx context.Context, guildID string, body map[string]any, reason string) (*Guild, error) {
	resp, err := c.Do(ctx, "PATCH", Path("/guilds/%s", guildID), body, WithReason(reason))
	if err != nil {
		return nil, err
	}
	var g Guild
	if err = DecodeJSON(resp, &g); err != nil {
		return nil, err
	}
	return &g, nil
}

// SetOnboarding toggles onboarding (the server-rules surface). Requires MANAGE_GUILD + MANAGE_ROLES.
func (c *Client) SetOnboarding(ctx context.Context, guildID string, enabled bool, reason string) error {
	body := map[string]any{"enabled": enabled}
	resp, err := c.Do(ctx, "PUT", Path("/guilds/%s/onboarding", guildID), body, WithReason(reason))
	if err != nil {
		return err
	}
	return DiscardBody(resp)
}

// SetWelcomeScreen toggles the welcome screen. Requires MANAGE_GUILD.
func (c *Client) SetWelcomeScreen(ctx context.Context, guildID string, enabled bool, description string, reason string) error {
	body := map[string]any{"enabled": enabled}
	if description != "" {
		body["description"] = description
	}
	resp, err := c.Do(ctx, "PATCH", Path("/guilds/%s/welcome-screen", guildID), body, WithReason(reason))
	if err != nil {
		return err
	}
	return DiscardBody(resp)
}

// SetMemberVerification modifies membership screening (server rules prompt).
// Endpoint is marked @unstable but functional.
func (c *Client) SetMemberVerification(ctx context.Context, guildID string, enabled bool, description string, reason string) error {
	body := map[string]any{"enabled": enabled, "form_fields": "[]"}
	if description != "" {
		body["description"] = description
	}
	resp, err := c.Do(ctx, "PATCH", Path("/guilds/%s/member-verification", guildID), body, WithReason(reason))
	if err != nil {
		return err
	}
	return DiscardBody(resp)
}

// SendMessage posts a plain message payload to a channel.
func (c *Client) SendMessage(ctx context.Context, channelID string, body any) error {
	resp, err := c.Do(ctx, "POST", Path("/channels/%s/messages", channelID), body)
	if err != nil {
		return err
	}
	return DiscardBody(resp)
}

// BulkDeleteMessages deletes 2-100 messages at once in a channel.
func (c *Client) BulkDeleteMessages(ctx context.Context, channelID string, ids []string) error {
	body := map[string]any{"messages": ids}
	resp, err := c.Do(ctx, "POST", Path("/channels/%s/messages/bulk-delete", channelID), body)
	if err != nil {
		return err
	}
	return DiscardBody(resp)
}

// CreateWebhook makes a webhook on a channel.
func (c *Client) CreateWebhook(ctx context.Context, channelID, name, reason string) (*Webhook, error) {
	body := map[string]any{"name": name}
	resp, err := c.Do(ctx, "POST", Path("/channels/%s/webhooks", channelID), body, WithReason(reason))
	if err != nil {
		return nil, err
	}
	var w Webhook
	if err := DecodeJSON(resp, &w); err != nil {
		return nil, err
	}
	return &w, nil
}

// ExecuteWebhook sends via webhook. wait=true so errors surface.
// No auth header — the webhook id+token in the path is the credential.
func (c *Client) ExecuteWebhook(ctx context.Context, webhookID, webhookToken string, body any) error {
	path := fmt.Sprintf("/webhooks/%s/%s?wait=true", webhookID, webhookToken)
	resp, err := c.Do(ctx, "POST", path, body, NoAuth())
	if err != nil {
		return err
	}
	return DiscardBody(resp)
}

// WaitSleep is exported for tests / orchestration pacing.
func WaitSleep(d time.Duration) { time.Sleep(d) }
