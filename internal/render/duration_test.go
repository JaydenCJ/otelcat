// Tests for human duration formatting — the single most-read number on
// every rendered line, so its rounding rules are pinned exactly.
package render

import "testing"

func TestFormatDurationNanoseconds(t *testing.T) {
	if got := FormatDuration(0); got != "0ns" {
		t.Fatalf("got %q", got)
	}
	if got := FormatDuration(850); got != "850ns" {
		t.Fatalf("got %q", got)
	}
}

func TestFormatDurationMicroseconds(t *testing.T) {
	cases := map[uint64]string{
		1_000:   "1µs",
		4_200:   "4.2µs",
		4_250:   "4.25µs",
		42_500:  "42.5µs",
		425_000: "425µs",
	}
	for ns, want := range cases {
		if got := FormatDuration(ns); got != want {
			t.Errorf("%dns: want %q, got %q", ns, want, got)
		}
	}
}

func TestFormatDurationMilliseconds(t *testing.T) {
	// 5.00ms and 50.0ms must print as 5ms / 50ms — trailing zeros are
	// visual noise on a column read hundreds of times per session.
	cases := map[uint64]string{
		1_000_000:   "1ms",
		5_000_000:   "5ms",
		12_300_000:  "12.3ms",
		50_000_000:  "50ms",
		88_900_000:  "88.9ms",
		128_400_000: "128ms",
		999_999_999: "999ms",
	}
	for ns, want := range cases {
		if got := FormatDuration(ns); got != want {
			t.Errorf("%dns: want %q, got %q", ns, want, got)
		}
	}
}

func TestFormatDurationSeconds(t *testing.T) {
	cases := map[uint64]string{
		1_000_000_000:  "1s",
		1_240_000_000:  "1.24s",
		12_500_000_000: "12.5s",
		59_900_000_000: "59.9s",
	}
	for ns, want := range cases {
		if got := FormatDuration(ns); got != want {
			t.Errorf("%dns: want %q, got %q", ns, want, got)
		}
	}
}

func TestFormatDurationMinutesAndHours(t *testing.T) {
	if got := FormatDuration(123_000_000_000); got != "2m03s" {
		t.Fatalf("got %q", got)
	}
	if got := FormatDuration(3_840_000_000_000); got != "1h04m" {
		t.Fatalf("got %q", got)
	}
}
