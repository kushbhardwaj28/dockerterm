package ui

import (
	"fmt"
	"image/color"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/docker/go-units"
)

// fit truncates (with …) or pads s to exactly w cells. s may contain ANSI.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", w-lipgloss.Width(s))
}

// fitRight is fit with the text pushed to the right edge.
func fitRight(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	return strings.Repeat(" ", w-lipgloss.Width(s)) + s
}

// spread puts left and right on one line of width w, right-aligned.
func spread(left, right string, w int) string {
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return fit(left, w)
	}
	return left + strings.Repeat(" ", gap) + right
}

func hline(w int) string { return sLine.Render(strings.Repeat("─", max(0, w))) }

// fillBg paints a whole line with a background, keeping inner styles.
func fillBg(s string, w int, bg color.Color) string {
	return lipgloss.NewStyle().Background(bg).Width(w).MaxWidth(w).Render(fit(s, w))
}

// box pads or clips content to exactly w×h cells.
func box(content string, w, h int) string {
	lines := strings.Split(content, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	for i := range lines {
		lines[i] = fit(lines[i], w)
	}
	for len(lines) < h {
		lines = append(lines, strings.Repeat(" ", w))
	}
	return strings.Join(lines, "\n")
}

func vline(h int) string {
	return sLine.Render(strings.TrimSuffix(strings.Repeat("│\n", max(1, h)), "\n"))
}

func bytesStr(n int64) string {
	if n < 0 {
		return "—"
	}
	return humanBytes(float64(n))
}

func ubytes(n uint64) string { return humanBytes(float64(n)) }

// humanBytes is a short binary size like the mockup: "18 MB", "1.8 GB", "342 MB".
func humanBytes(n float64) string {
	unitNames := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	i := 0
	for n >= 1024 && i < len(unitNames)-1 {
		n /= 1024
		i++
	}
	switch {
	case i == 0:
		return fmt.Sprintf("%.0f B", n)
	case n >= 100:
		return fmt.Sprintf("%.0f %s", n, unitNames[i])
	default:
		return strings.TrimSuffix(fmt.Sprintf("%.1f", n), ".0") + " " + unitNames[i]
	}
}

func pct(v float64) string { return fmt.Sprintf("%.1f%%", v) }

func ago(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "—"
	}
	if now.IsZero() {
		now = time.Now()
	}
	return units.HumanDuration(now.Sub(t)) + " ago"
}

const sparkRunes = "▁▂▃▄▅▆▇█"

// sparkline draws values (0–100) as block characters, newest on the right.
func sparkline(values []float64, w int) string {
	if len(values) > w {
		values = values[len(values)-w:]
	}
	runes := []rune(sparkRunes)
	var b strings.Builder
	for _, v := range values {
		i := int(v / 100 * float64(len(runes)-1))
		b.WriteRune(runes[min(len(runes)-1, max(0, i))])
	}
	return b.String()
}

// meter is a horizontal bar filled to p percent.
func meter(p float64, w int, fill color.Color) string {
	n := int(p / 100 * float64(w))
	n = min(w, max(0, n))
	if p > 0 && n == 0 {
		n = 1
	}
	return lipgloss.NewStyle().Foreground(fill).Render(strings.Repeat("█", n)) +
		lipgloss.NewStyle().Foreground(cTrackBg).Render(strings.Repeat("█", w-n))
}

func dash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}
