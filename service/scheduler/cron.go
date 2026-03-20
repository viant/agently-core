package scheduler

import (
	"fmt"
	"strings"
	"time"
)

type cronSpec struct {
	min, hour, dom, mon, dow map[int]bool
}

func parseCron(expr string) (*cronSpec, error) {
	parts := strings.Fields(expr)
	if len(parts) < 5 {
		return nil, fmt.Errorf("invalid cron expr: %s", expr)
	}
	min, err := parseCronField(parts[0], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("minute: %w", err)
	}
	hour, err := parseCronField(parts[1], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("hour: %w", err)
	}
	dom, err := parseCronField(parts[2], 1, 31)
	if err != nil {
		return nil, fmt.Errorf("dom: %w", err)
	}
	mon, err := parseCronField(parts[3], 1, 12)
	if err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	dow, err := parseCronField(parts[4], 0, 6)
	if err != nil {
		return nil, fmt.Errorf("dow: %w", err)
	}
	return &cronSpec{min: min, hour: hour, dom: dom, mon: mon, dow: dow}, nil
}

func parseCronField(s string, min, max int) (map[int]bool, error) {
	s = strings.TrimSpace(s)
	set := map[int]bool{}
	add := func(v int) {
		if v >= min && v <= max {
			set[v] = true
		}
	}
	if s == "*" || s == "?" {
		for i := min; i <= max; i++ {
			set[i] = true
		}
		return set, nil
	}
	for _, tok := range strings.Split(s, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		step := 1
		if parts := strings.Split(tok, "/"); len(parts) == 2 {
			tok = parts[0]
			if v, err := atoiSafe(parts[1]); err == nil && v > 0 {
				step = v
			}
		}
		if tok == "*" {
			for i := min; i <= max; i += step {
				set[i] = true
			}
			continue
		}
		if strings.Contains(tok, "-") {
			rs := strings.SplitN(tok, "-", 2)
			a, err1 := atoiSafe(rs[0])
			b, err2 := atoiSafe(rs[1])
			if err1 != nil || err2 != nil {
				return nil, fmt.Errorf("invalid range: %s", tok)
			}
			if a > b {
				a, b = b, a
			}
			for i := a; i <= b; i += step {
				add(i)
			}
			continue
		}
		if v, err := atoiSafe(tok); err == nil {
			add(v)
			continue
		}
		return nil, fmt.Errorf("invalid token: %s", tok)
	}
	if len(set) == 0 {
		return nil, fmt.Errorf("empty set")
	}
	return set, nil
}

func atoiSafe(s string) (int, error) {
	var n int
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("invalid int: %s", s)
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

func cronMatch(spec *cronSpec, t time.Time) bool {
	if !spec.min[t.Minute()] {
		return false
	}
	if !spec.hour[t.Hour()] {
		return false
	}
	if !spec.dom[t.Day()] {
		return false
	}
	if !spec.mon[int(t.Month())] {
		return false
	}
	if !spec.dow[int(t.Weekday())] {
		return false
	}
	return true
}

func cronNext(spec *cronSpec, from time.Time) time.Time {
	t := from.Add(time.Minute)
	for i := 0; i < 525600; i++ {
		if cronMatch(spec, t) {
			return t
		}
		t = t.Add(time.Minute)
	}
	return t
}
