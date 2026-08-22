// Package ui renders the terminal banner, gradients, prompts, and live counters.
package ui

import (
	"bufio"
	"fmt"
	"math/rand"
	"os"
	"strings"

	"github.com/common-nighthawk/go-figure"
)

// Banner picks a random ASCII font and renders the name with a random gradient.
func Banner(name string) {
	fonts := []string{
		"Standard", "Bold", "Block", "Bubble", "Digital",
		"Ivy", "Mini", "Script", "Shadow", "Slant", "Speed", "Star Wars",
	}
	f := fonts[rand.Intn(len(fonts))]
	fig := figure.NewFigure(strings.ToUpper(name), f, true)
	lines := strings.Split(fig.String(), "\n")

	hue := rand.Float64() * 360 // random hue for the gradient
	for i, line := range lines {
		if line == "" {
			continue
		}
		t := float64(i) / float64(max(1, len(lines)-1))
		r, g, b := hslToRGB(hue, 0.85, 0.35+t*0.35)
		fmt.Printf("\x1b[38;2;%d;%d;%dm%s\x1b[0m\n", r, g, b, line)
	}
	fmt.Println()
}

// AccentColor returns the hue used by the last Banner call as a Discord int color.
// Call right after Banner to reuse the same hue in the raid payload.
var AccentColor int

// PickAccent stores the accent color used for the banner gradient so payloads
// can reuse it.
func PickAccent() int {
	AccentColor = int(hueToRGB24(rand.Float64() * 360))
	return AccentColor
}

// Info prints an info line with a colored bullet.
func Info(format string, args ...any) { line("•", "36", format, args...) }

// Ok prints a success line.
func Ok(format string, args ...any) { line("✓", "32", format, args...) }

// Warn prints a warning.
func Warn(format string, args ...any) { line("!", "33", format, args...) }

// Err prints an error.
func Err(format string, args ...any) { line("✗", "31", format, args...) }

// Dim prints low-emphasis text.
func Dim(format string, args ...any) {
	fmt.Printf("\x1b[2m%s\x1b[0m\n", fmt.Sprintf(format, args...))
}

func line(sym, code, format string, args ...any) {
	fmt.Printf("\x1b[38;5;%sm %s \x1b[0m%s\n", code, sym, fmt.Sprintf(format, args...))
}

// Confirm shows a y/N prompt; returns true only on explicit yes/y.
func Confirm(prompt string) bool {
	fmt.Printf("\x1b[1m%s [y/N]: \x1b[0m", prompt)
	sc := bufio.NewScanner(os.Stdin)
	if !sc.Scan() {
		return false
	}
	s := strings.ToLower(strings.TrimSpace(sc.Text()))
	return s == "y" || s == "yes"
}

// Progress is a simple single-line counter that redraws itself.
type Progress struct {
	Label string
	Done  int64
	Total int64
}

// Tick increments done count and redraws.
func (p *Progress) Tick(n int64) {
	p.Done += n
	percent := 0
	if p.Total > 0 {
		percent = int(float64(p.Done) / float64(p.Total) * 100)
	}
	fmt.Printf("\r\x1b[K %s: %d/%d (%d%%)", p.Label, p.Done, p.Total, percent)
}

// Finish prints newline after ticks complete.
func (p *Progress) Finish() { fmt.Println() }
