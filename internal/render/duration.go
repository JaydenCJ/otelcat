package render

import "fmt"

// FormatDuration renders a nanosecond duration the way humans scan a
// trace: at most three significant digits, one unit, no trailing noise.
// Examples: 0ns, 850ns, 4.2µs, 12.3ms, 1.24s, 2m03s, 1h04m.
func FormatDuration(ns uint64) string {
	switch {
	case ns < 1_000:
		return fmt.Sprintf("%dns", ns)
	case ns < 1_000_000:
		return scaled(ns, 1_000, "µs")
	case ns < 1_000_000_000:
		return scaled(ns, 1_000_000, "ms")
	case ns < 60_000_000_000:
		return scaled(ns, 1_000_000_000, "s")
	case ns < 3_600_000_000_000:
		m := ns / 60_000_000_000
		s := (ns % 60_000_000_000) / 1_000_000_000
		return fmt.Sprintf("%dm%02ds", m, s)
	default:
		h := ns / 3_600_000_000_000
		m := (ns % 3_600_000_000_000) / 60_000_000_000
		return fmt.Sprintf("%dh%02dm", h, m)
	}
}

// scaled prints value/div with enough decimals to keep three significant
// digits, then strips a trailing ".0" / ".00" so 5.00ms reads as 5ms.
func scaled(ns, div uint64, unit string) string {
	whole := ns / div
	frac := ns % div
	switch {
	case whole >= 100:
		return fmt.Sprintf("%d%s", whole, unit)
	case whole >= 10:
		d := frac * 10 / div
		if d == 0 {
			return fmt.Sprintf("%d%s", whole, unit)
		}
		return fmt.Sprintf("%d.%d%s", whole, d, unit)
	default:
		d := frac * 100 / div
		if d == 0 {
			return fmt.Sprintf("%d%s", whole, unit)
		}
		if d%10 == 0 {
			return fmt.Sprintf("%d.%d%s", whole, d/10, unit)
		}
		return fmt.Sprintf("%d.%02d%s", whole, d, unit)
	}
}
