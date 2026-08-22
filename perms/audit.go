// Package perms audits all tokens against a guild and reports deficiencies.
package perms

import (
	"context"
	"fmt"
	"sync"

	"github.com/M1noa/wrack/api"
	"github.com/M1noa/wrack/token"
)

// Report summarizes audit results across all tokens.
type Report struct {
	OK      []*token.Token
	Bad     []*token.Token
	Webhook bool // true if at least one token has MANAGE_WEBHOOKS
	Mixed   bool // some tokens have webhooks, some don't
}

// Audit runs token.Audit for each raw token in parallel and computes perms.
func Audit(ctx context.Context, raws []string, guildID string, roles []api.Role, ownerID string) *Report {
	rep := &Report{}
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, raw := range raws {
		wg.Add(1)
		go func(raw string) {
			defer wg.Done()
			t, err := token.Audit(ctx, raw, guildID, func(r string) *api.Client {
				return api.NewClient(r, nil)
			})
			if err == nil && t.InGuild {
				t.ComputePerms(roles, ownerID)
			} else if err != nil {
				t.Errors = append(t.Errors, err.Error())
			}
			mu.Lock()
			if t.InGuild && len(t.Errors) == 0 {
				rep.OK = append(rep.OK, t)
				if t.HasPerm(api.PermManageWebhooks) {
					rep.Webhook = true
				}
			} else {
				rep.Bad = append(rep.Bad, t)
			}
			mu.Unlock()
		}(raw)
	}
	wg.Wait()

	hasHook := false
	hasNot := false
	for _, t := range rep.OK {
		if t.HasPerm(api.PermManageWebhooks) {
			hasHook = true
		} else {
			hasNot = true
		}
	}
	rep.Mixed = hasHook && hasNot
	return rep
}

// Print renders the audit report to stdout.
func (r *Report) Print(toolName string) {
	fmt.Printf("\n\x1b[1m%s\x1b[0m\n", "── token audit ──")
	for _, t := range r.OK {
		missing := t.Missing(token.RequiredPerms)
		status := "\x1b[32mok\x1b[0m"
		if len(missing) > 0 {
			status = fmt.Sprintf("\x1b[33mpartial\x1b[0m (lacks: %s)", join(missing))
		}
		fmt.Printf("  %s  %-20s %s\n", status, maskToken(t.Raw), kindLabel(t.Kind, t.Username))
	}
	for _, t := range r.Bad {
		fmt.Printf("  \x1b[31mbad \x1b[0m %-20s %s\n", maskToken(t.Raw), join(t.Errors))
	}
	fmt.Println()
}

func kindLabel(kind, username string) string {
	k := kind
	if k == "" {
		k = "?"
	}
	return fmt.Sprintf("(%s %s)", k, username)
}

func maskToken(s string) string {
	if len(s) <= 12 {
		return s[:3] + "***"
	}
	return s[:8] + "***" + s[len(s)-4:]
}

func join(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
