package duration

import (
	"errors"
	"testing"
	"time"
)

func TestParse(t *testing.T) {
	ok := map[string]time.Duration{
		"25":         25 * time.Minute,
		"25m":        25 * time.Minute,
		"1h30m":      90 * time.Minute,
		"1h 30m":     90 * time.Minute,
		"1ч 30м":     90 * time.Minute,
		"2 часа":     2 * time.Hour,
		"45s":        45 * time.Second,
		"10 сек":     10 * time.Second,
		"1д":         24 * time.Hour,
		"  15 МИН  ": 15 * time.Minute,
		"1h 0m 30s":  time.Hour + 30*time.Second,
		"7d":         Max,
	}
	for in, want := range ok {
		got, err := Parse(in)
		if err != nil || got != want {
			t.Errorf("Parse(%q) = %v, %v; want %v", in, got, err, want)
		}
	}

	bad := map[string]error{
		"":          ErrEmpty,
		"abc":       ErrBadInput,
		"10x":       ErrBadInput,
		"10m abc":   ErrBadInput,
		"abc 10m":   ErrBadInput,
		"3s":        ErrTooShort,
		"0":         ErrTooShort,
		"8d":        ErrTooLong,
		"99999999h": ErrTooLong,
	}
	for in, want := range bad {
		if _, err := Parse(in); !errors.Is(err, want) {
			t.Errorf("Parse(%q) error = %v; want %v", in, err, want)
		}
	}
}

func TestFormat(t *testing.T) {
	cases := map[time.Duration]string{
		45 * time.Second:                "45 с",
		25 * time.Minute:                "25 мин",
		90*time.Minute + 20*time.Second: "1 ч 30 мин",
		26 * time.Hour:                  "1 д 2 ч",
		0:                               "0 с",
	}
	for in, want := range cases {
		if got := Format(in); got != want {
			t.Errorf("Format(%v) = %q; want %q", in, got, want)
		}
	}
}
