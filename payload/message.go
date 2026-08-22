// Package payload handles discohook JSON parsing, webhook message building,
// and runtime-generated blank media for emojis/stickers/sounds.
package payload

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
)

// Message is a generic Discord message payload (content, embeds, components).
type Message struct {
	Content    string          `json:"content,omitempty"`
	Embeds     json.RawMessage `json:"embeds,omitempty"`
	Components json.RawMessage `json:"components,omitempty"`
	Username   string          `json:"username,omitempty"`
	AvatarURL  string          `json:"avatar_url,omitempty"`
	TTS        bool            `json:"tts,omitempty"`
	Flags      int             `json:"flags,omitempty"`
}

// Load reads a discohook "Plain JSON" export from disk and returns a Message
// ready to send via webhook or channel message. Detects components-v2 payloads
// (presence of top-level "components") and sets the IS_COMPONENTS_V2 flag (32768).
func Load(path string) (*Message, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("payload: read %s: %w", path, err)
	}
	return Parse(b)
}

// Parse builds a Message from raw discohook JSON bytes.
func Parse(b []byte) (*Message, error) {
	var m Message
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("payload: parse: %w", err)
	}
	if len(m.Components) > 0 && m.Components[0] != 'n' { // not null
		m.Flags |= 32768 // IS_COMPONENTS_V2
	}
	return &m, nil
}

// HasWebhookFields reports if the message overrides webhook identity.
func (m *Message) HasWebhookFields() bool {
	return m.Username != "" || m.AvatarURL != ""
}

// BlankPNG returns a transparent NxN PNG as bytes. Meets Discord emoji and
// sticker minimums when N >= 320.
func BlankPNG(n int) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	transparent := color.RGBA{0, 0, 0, 0}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			img.Set(x, y, transparent)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// DataURI wraps raw image bytes as a data:image/png;base64 URI for the API.
func DataURI(raw []byte) string {
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)
}

// SilentMP3 returns a tiny valid MP3 frame sequence (~26ms of silence per frame,
// repeated to fill ~0.1s). Meets soundboard's mp3 requirement at minimal size.
func SilentMP3() []byte {
	// MPEG-1 Layer III, 44.1kHz, mono, 32kbps: frame size = 104 bytes.
	// Header: 0xFFFB9064 (sync, layer3, no CRC, 32kbps, 44.1kHz, mono).
	frame := make([]byte, 104)
	frame[0] = 0xFF
	frame[1] = 0xFB
	frame[2] = 0x90
	frame[3] = 0x64
	// Rest is zero (silent). Repeat 4 frames ≈ 104ms.
	out := make([]byte, 0, len(frame)*4)
	for i := 0; i < 4; i++ {
		out = append(out, frame...)
	}
	return out
}

// DefaultRaidMessage builds the built-in raid payload when --message isn't given.
// Uses components v2 with an accent color matching the CLI gradient hue.
func DefaultRaidMessage(name string, accentColor int) *Message {
	container := map[string]any{
		"type":        17, // CONTAINER
		"accent_color": accentColor,
		"components": []any{
			map[string]any{"type": 10, "content": "# " + name},
			map[string]any{"type": 14, "divider": true, "spacing": 1},
			map[string]any{"type": 10, "content": "get wracked."},
			map[string]any{
				"type": 12, // MEDIA_GALLERY
				"items": []any{
					map[string]any{"media": map[string]any{"url": ""}},
				},
			},
		},
	}
	comps, _ := json.Marshal([]any{container})
	return &Message{
		Flags:      32768,
		Components: comps,
		Content:    "",
	}
}

// FallbackEmbedMessage is a classic-embed variant used when webhooks aren't
// available or the target client doesn't render v2 well.
func FallbackEmbedMessage(name string, accentColor int) *Message {
	embeds := json.RawMessage(fmt.Sprintf(
		`[{"title":"%s","description":"get wracked.","color":%d}]`, name, accentColor))
	return &Message{Embeds: embeds}
}
