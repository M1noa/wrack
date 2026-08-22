// Package api wraps the Discord HTTP API. Each Client owns its own
// http.Transport (so proxy + keepalive are isolated), its own bucket store
// for rate limits, and its own token. All destructive calls go through Do().
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Discord API version.
const Version = "v10"
const BaseURL = "https://discord.com/api/" + Version

// Permission bits.
const (
	PermKickMembers             = 1 << 1
	PermBanMembers              = 1 << 2
	PermAdministrator           = 1 << 3
	PermManageChannels          = 1 << 4
	PermManageGuild             = 1 << 5
	PermManageMessages          = 1 << 13
	PermManageRoles             = 1 << 28
	PermManageWebhooks          = 1 << 29
	PermManageEmojisAndStickers = 1 << 30
)

// IsComponentsV2 is the message flag enabling components v2 layout.
const IsComponentsV2 = 1 << 15 // 32768

// Client is one token's view of Discord.
type Client struct {
	Token       string // bot token or user token (no prefix)
	Kind        string // "bot" | "user" (set after classify)
	UserID      string
	HTTP        *http.Client
	Rotator     ProxyRotator // returns next http.Transport per request
	mu          sync.Mutex
	buckets     map[string]*bucket
	botPerms    int64 // cached after audit
	PremiumTier int   // guild boost tier (set by recon after GetGuild)
}

// ProxyRotator yields an http.Transport per request (for round-robin proxy use).
type ProxyRotator interface {
	Next() Proxy
}

// Proxy is the minimal interface a proxy implementation must satisfy.
type Proxy interface {
	Transport() *http.Transport
	Healthy() bool
	MarkFail()
	MarkOK()
}

// NewClient builds a Discord client. The Rotator may be nil for direct connections.
func NewClient(token string, rotator ProxyRotator) *Client {
	c := &Client{
		Token:   token,
		Rotator: rotator,
		buckets: make(map[string]*bucket),
	}
	c.HTTP = &http.Client{Timeout: 15 * time.Second}
	c.tune()
	return c
}

// tune maximizes connection throughput: big idle pool, HTTP/2 multiplexing,
// so dozens of concurrent workers share few TCP handshakes.
func (c *Client) tune() {
	if tr, ok := c.HTTP.Transport.(*http.Transport); ok && tr != nil {
		tr.MaxIdleConns = 0
		tr.MaxIdleConnsPerHost = 256
		tr.ForceAttemptHTTP2 = true
		return
	}
	clone := http.DefaultTransport.(*http.Transport).Clone()
	clone.MaxIdleConns = 0
	clone.MaxIdleConnsPerHost = 256
	clone.ForceAttemptHTTP2 = true
	c.HTTP.Transport = clone
}

// SetTransport pins a specific transport (used when no proxy rotation).
func (c *Client) SetTransport(t *http.Transport) {
	c.HTTP = &http.Client{Timeout: 15 * time.Second, Transport: t}
}

func (c *Client) transport() http.RoundTripper {
	if c.Rotator == nil {
		return c.HTTP.Transport
	}
	if p := c.Rotator.Next(); p != nil {
		return p.Transport()
	}
	return c.HTTP.Transport
}

// authHeader returns the Authorization value (Bot prefix only for bot tokens).
func (c *Client) authHeader() string {
	if c.Kind == "bot" {
		return "Bot " + c.Token
	}
	return c.Token
}

// Do is the request core with smart-hammer rate limiting. Successful
// requests fire back-to-back with zero artificial delay. On 429 we wait
// exactly the server-stated reset (waiting less just burns cycles and
// escalates to hour-long global bans — empirically verified), then resume
// instantly. Escalated punishments (>30s) abort instead of cooking the token.
func (c *Client) Do(ctx context.Context, method, path string, body any, opts ...ReqOpt) (*http.Response, error) {
	r := newReq(method, path, body, opts...)
	for attempt := 0; attempt < 100000; attempt++ {
		resp, err := c.doOnce(ctx, r)
		if err != nil {
			return nil, err
		}
		if b := resp.Header.Get("X-RateLimit-Bucket"); b != "" {
			c.noteBucket(b, parseFloatReset(resp))
		}
		if resp.StatusCode == 429 {
			retryAfter := parseRetryAfter(resp)
			resp.Body.Close()
			if retryAfter > 30*time.Second {
				return nil, fmt.Errorf("api: escalated global ratelimit (%s) on %s %s", retryAfter, method, path)
			}
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(retryAfter + 25*time.Millisecond):
			}
			continue
		}
		if resp.StatusCode >= 500 && attempt < 99990 {
			resp.Body.Close()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(25 * time.Millisecond):
			}
			continue
		}
		return resp, nil
	}
	return nil, fmt.Errorf("api: gave up on %s %s", method, path)
}

func (c *Client) doOnce(ctx context.Context, r *req) (*http.Response, error) {
	var bodyReader io.Reader
	contentType := ""
	if r.fileBytes != nil {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		for k, v := range r.formFields {
			w.WriteField(k, v)
		}
		part, err := w.CreateFormFile(r.fileField, r.fileName)
		if err != nil {
			return nil, err
		}
		if _, err := part.Write(r.fileBytes); err != nil {
			return nil, err
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		bodyReader = &buf
		contentType = w.FormDataContentType()
	} else if r.body != nil {
		buf, err := json.Marshal(r.body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(buf)
		contentType = "application/json"
	}
	url := BaseURL + r.path
	if r.query != "" {
		url += "?" + r.query
	}
	req, err := http.NewRequestWithContext(ctx, r.method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	if !r.noAuth {
		req.Header.Set("Authorization", c.authHeader())
	}
	req.Header.Set("User-Agent", "wrack")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if r.reason != "" {
		req.Header.Set("X-Audit-Log-Reason", r.reason)
	}
	rt := c.transport()
	if rt == nil {
		rt = http.DefaultTransport
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	// Track bucket.
	if b := resp.Header.Get("X-RateLimit-Bucket"); b != "" {
		reset := parseRetryAfter(resp)
		if reset == 0 {
			reset = parseFloatReset(resp)
		}
		c.noteBucket(b, reset)
	}
	return resp, nil
}

func parseRetryAfter(resp *http.Response) time.Duration {
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0
	}
	var secs float64
	fmt.Sscanf(v, "%f", &secs)
	if secs > 0 {
		return time.Duration(secs * float64(time.Second))
	}
	return 500 * time.Millisecond
}

func parseFloatReset(resp *http.Response) time.Duration {
	v := resp.Header.Get("X-RateLimit-Reset")
	if v == "" {
		return 0
	}
	var reset float64
	fmt.Sscanf(v, "%f", &reset)
	if reset == 0 {
		return 0
	}
	now := float64(time.Now().UnixMilli()) / 1000.0
	diff := reset - now
	if diff <= 0 {
		return 0
	}
	return time.Duration(diff * float64(time.Second))
}

// bucket holds the locked-until time for a single bucket id.
type bucket struct {
	mu       sync.Mutex
	lockTill time.Time
}

func (c *Client) lockBucket(id string, dur time.Duration) {
	c.mu.Lock()
	b, ok := c.buckets[id]
	if !ok {
		b = &bucket{}
		c.buckets[id] = b
	}
	c.mu.Unlock()
	b.mu.Lock()
	if time.Now().Add(dur).After(b.lockTill) {
		b.lockTill = time.Now().Add(dur)
	}
	b.mu.Unlock()
}

func (c *Client) bucketLocked(id string) (time.Duration, bool) {
	c.mu.Lock()
	b, ok := c.buckets[id]
	c.mu.Unlock()
	if !ok {
		return 0, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.lockTill.IsZero() {
		return 0, false
	}
	d := time.Until(b.lockTill)
	if d <= 0 {
		return 0, false
	}
	return d, true
}

func (c *Client) noteBucket(id string, reset time.Duration) {
	if reset <= 0 {
		return
	}
	c.lockBucket(id, reset)
}

// DecodeJSON unmarshals a successful response body.
func DecodeJSON(resp *http.Response, out any) error {
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out)
}

// DiscardBody closes & discards the body (for 204s etc).
func DiscardBody(resp *http.Response) error {
	defer resp.Body.Close()
	_, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	return err
}

// IsUserToken returns true if the token looks like a user token (no dot in
// the prefix region). Bot tokens contain a dot before the first dot: "abc.def".
// User tokens contain two dots: "abc.def.ghi". We use that as a hint, then
// confirm by hitting /users/@me.
func LooksLikeUserToken(t string) bool {
	// Strip any explicit prefix.
	t = strings.TrimSpace(t)
	t = strings.TrimPrefix(t, "Bot ")
	t = strings.TrimPrefix(t, "Bearer ")
	return strings.Count(t, ".") >= 2
}
