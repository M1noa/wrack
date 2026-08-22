package ui

import "math"

// hslToRGB converts HSL to 8-bit RGB.
func hslToRGB(h, s, l float64) (int, int, int) {
	var r, g, b float64
	if s == 0 {
		r = l
		g = l
		b = l
	} else {
		q := l
		if l >= 0.5 {
			q = l + s - l*s
		} else {
			q = l * (1 + s)
		}
		p := 2*l - q
		r = hue2rgb(p, q, h+1.0/3.0)
		g = hue2rgb(p, q, h)
		b = hue2rgb(p, q, h-1.0/3.0)
	}
	return clamp255(r * 255), clamp255(g * 255), clamp255(b * 255)
}

// hueToRGB24 packs an RGB triple into Discord's integer color format.
func hueToRGB24(h float64) int {
	r, g, b := hslToRGB(h, 0.85, 0.55)
	return (r << 16) | (g << 8) | b
}

func hue2rgb(p, q, t float64) float64 {
	if t < 0 {
		t += 1
	}
	if t > 1 {
		t -= 1
	}
	switch {
	case t < 1.0/6.0:
		return p + (q-p)*6*t
	case t < 0.5:
		return q
	case t < 2.0/3.0:
		return p + (q-p)*(2.0/3.0-t)*6
	default:
		return p
	}
}

func clamp255(v float64) int {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return int(v)
}

// max helper (Go 1.21+ has builtin but keep local for clarity).
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var _ = math.Pi // keep math import if unused elsewhere
