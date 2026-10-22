package bot

import (
	"testing"
	"time"
)

func TestSplitLength(t *testing.T) {
	cases := []struct {
		in    string
		d     time.Duration
		label string
	}{
		{"25", 25 * time.Minute, ""},
		{"1h30m Созвон с командой", 90 * time.Minute, "Созвон с командой"},
		{"1ч 30м чай", 90 * time.Minute, "чай"},
		{"2 часа", 2 * time.Hour, ""},
		{"10 Духовка", 10 * time.Minute, "Духовка"},
	}
	for _, c := range cases {
		d, label, err := splitLength(c.in)
		if err != nil || d != c.d || label != c.label {
			t.Errorf("splitLength(%q) = %v, %q, %v; want %v, %q", c.in, d, label, err, c.d, c.label)
		}
	}
	for _, in := range []string{"", "чай", "3s"} {
		if _, _, err := splitLength(in); err == nil {
			t.Errorf("splitLength(%q) accepted", in)
		}
	}
}
