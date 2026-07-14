package render

// Theme maps semantic roles to ANSI escape sequences. A disabled theme
// returns empty strings everywhere, so rendering code never branches on
// color support — it always wraps, and the wrap may be a no-op.
type Theme struct {
	enabled bool
}

// NewTheme returns a Theme that emits ANSI codes iff enabled.
func NewTheme(enabled bool) Theme { return Theme{enabled: enabled} }

// Enabled reports whether this theme emits ANSI codes.
func (t Theme) Enabled() bool { return t.enabled }

func (t Theme) wrap(code, s string) string {
	if !t.enabled || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// Semantic roles used by the renderers. The palette sticks to the basic
// 16 colors so it degrades well on every terminal.

// Header styles trace/batch header lines (bold).
func (t Theme) Header(s string) string { return t.wrap("1", s) }

// Dim styles secondary detail: ids, scopes, attribute keys.
func (t Theme) Dim(s string) string { return t.wrap("2", s) }

// Name styles span names (cyan).
func (t Theme) Name(s string) string { return t.wrap("36", s) }

// Duration styles durations (yellow).
func (t Theme) Duration(s string) string { return t.wrap("33", s) }

// Kind styles span kind tags (magenta).
func (t Theme) Kind(s string) string { return t.wrap("35", s) }

// OK styles success markers (green).
func (t Theme) OK(s string) string { return t.wrap("32", s) }

// Error styles error markers (bold red).
func (t Theme) Error(s string) string { return t.wrap("1;31", s) }

// Warn styles warning-level output (yellow).
func (t Theme) Warn(s string) string { return t.wrap("33", s) }

// Service styles service names (green).
func (t Theme) Service(s string) string { return t.wrap("32", s) }
