// Package duration parses timer lengths the way people type them in a chat.
package duration

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	Min = 5 * time.Second
	Max = 7 * 24 * time.Hour
)

var (
	ErrEmpty    = errors.New("пустая длительность")
	ErrBadInput = errors.New("не понял длительность: примеры 25, 25m, 1h30m, 1ч 30м, 45с")
	ErrTooShort = errors.New("минимум 5 секунд")
	ErrTooLong  = errors.New("максимум 7 дней")
)

// part is a number followed by a unit: 1h, 30m, 45s, 2д, 1ч, 30мин, 10 сек.
var part = regexp.MustCompile(`(\d+)\s*([a-zа-яё]+)`)

var units = map[string]time.Duration{
	"d": 24 * time.Hour, "д": 24 * time.Hour, "дн": 24 * time.Hour, "день": 24 * time.Hour, "дня": 24 * time.Hour, "дней": 24 * time.Hour,
	"h": time.Hour, "ч": time.Hour, "час": time.Hour, "часа": time.Hour, "часов": time.Hour,
	"m": time.Minute, "min": time.Minute, "м": time.Minute, "мин": time.Minute, "минута": time.Minute, "минуты": time.Minute, "минут": time.Minute,
	"s": time.Second, "sec": time.Second, "с": time.Second, "сек": time.Second, "секунд": time.Second, "секунды": time.Second,
}

// Parse accepts a bare number of minutes ("25") or a sequence of number+unit pairs
// in English or Russian ("1h30m", "1ч 30м", "2 часа", "45s").
func Parse(s string) (time.Duration, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0, ErrEmpty
	}

	var d time.Duration
	if n, err := strconv.Atoi(s); err == nil {
		if n > int(Max/time.Minute) {
			return 0, ErrTooLong
		}
		d = time.Duration(n) * time.Minute
	} else {
		matches := part.FindAllStringSubmatchIndex(s, -1)
		if matches == nil {
			return 0, ErrBadInput
		}
		// Everything between the matches must be spaces, otherwise the input has garbage in it.
		last := 0
		for _, m := range matches {
			if strings.TrimSpace(s[last:m[0]]) != "" {
				return 0, ErrBadInput
			}
			n, err := strconv.Atoi(s[m[2]:m[3]])
			if err != nil {
				return 0, ErrBadInput
			}
			unit, ok := units[s[m[4]:m[5]]]
			if !ok {
				return 0, ErrBadInput
			}
			if n > int(Max/unit) { // checked before multiplying to avoid overflow
				return 0, ErrTooLong
			}
			d += time.Duration(n) * unit
			if d > Max {
				return 0, ErrTooLong
			}
			last = m[1]
		}
		if strings.TrimSpace(s[last:]) != "" {
			return 0, ErrBadInput
		}
	}

	switch {
	case d < Min:
		return 0, ErrTooShort
	case d > Max:
		return 0, ErrTooLong
	}
	return d, nil
}

// Format prints a duration in short Russian form: "1 ч 30 мин", "45 с".
func Format(d time.Duration) string {
	d = d.Round(time.Second)
	if d < time.Second {
		return "0 с"
	}
	days := d / (24 * time.Hour)
	d -= days * 24 * time.Hour
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	sec := d / time.Second

	var parts []string
	add := func(v time.Duration, unit string) {
		if v > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", v, unit))
		}
	}
	add(days, "д")
	add(h, "ч")
	add(m, "мин")
	if days == 0 && h == 0 {
		add(sec, "с")
	}
	return strings.Join(parts, " ")
}
