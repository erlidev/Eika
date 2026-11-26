package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	// The runtime image may not have the system time zone database.
	_ "time/tzdata"
)

// Schedule is a one-shot time or a five-field cron schedule.
type Schedule struct {
	at     time.Time
	loc    *time.Location
	fields [5]field
	expr   string
}
type field struct {
	values   [60]bool
	wildcard bool
}

// Parse validates an at or cron expression and its IANA time zone.
func Parse(kind, expr, zone string) (Schedule, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil || zone == "" || zone == "Local" {
		return Schedule{}, fmt.Errorf("invalid time zone %q", zone)
	}
	s := Schedule{loc: loc, expr: expr}
	if kind == "at" {
		s.at, err = time.Parse(time.RFC3339, expr)
		if err != nil {
			return Schedule{}, fmt.Errorf("parse time: %w", err)
		}
		s.at = s.at.UTC()
		return s, nil
	}
	if kind != "cron" {
		return Schedule{}, fmt.Errorf("invalid schedule kind %q", kind)
	}
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return Schedule{}, fmt.Errorf("cron requires five fields")
	}
	mins := [5]int{0, 0, 1, 1, 0}
	maxs := [5]int{59, 23, 31, 12, 7}
	for i, part := range parts {
		f, e := parseField(part, mins[i], maxs[i], i)
		if e != nil {
			return Schedule{}, fmt.Errorf("parse cron field %d: %w", i+1, e)
		}
		s.fields[i] = f
	}
	return s, nil
}
func parseField(input string, lower, upper, index int) (field, error) {
	var f field
	f.wildcard = strings.HasPrefix(input, "*")
	number := func(v string) (int, error) {
		v = strings.ToLower(v)
		names := []string(nil)
		if index == 3 {
			names = []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}
		}
		if index == 4 {
			names = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
		}
		for i, name := range names {
			if v == name {
				if index == 3 {
					return i + 1, nil
				}
				return i, nil
			}
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < lower || n > upper {
			return 0, fmt.Errorf("invalid value %q", v)
		}
		return n, nil
	}
	for _, item := range strings.Split(input, ",") {
		p := strings.Split(item, "/")
		if len(p) > 2 {
			return f, fmt.Errorf("invalid step")
		}
		step := 1
		if len(p) == 2 {
			n, e := strconv.Atoi(p[1])
			if e != nil || n < 1 {
				return f, fmt.Errorf("invalid step")
			}
			step = n
		}
		lo, hi := lower, upper
		if p[0] != "*" {
			bounds := strings.Split(p[0], "-")
			if len(bounds) > 2 {
				return f, fmt.Errorf("invalid range")
			}
			var e error
			lo, e = number(bounds[0])
			if e != nil {
				return f, e
			}
			hi = lo
			if len(bounds) == 2 {
				hi, e = number(bounds[1])
				if e != nil {
					return f, e
				}
			} else if len(p) == 2 {
				hi = upper
			}
			if lo > hi {
				return f, fmt.Errorf("invalid range")
			}
		}
		for n := lo; n <= hi; {
			if index == 4 && n == 7 {
				f.values[0] = true
			} else {
				f.values[n] = true
			}
			if step > hi-n {
				break
			}
			n += step
		}
	}
	return f, nil
}

// Next returns the first occurrence strictly after after, or zero if none exists.
func (s Schedule) Next(after time.Time) time.Time {
	if !s.at.IsZero() {
		if s.at.After(after) {
			return s.at
		}
		return time.Time{}
	}
	if s.loc == nil {
		return time.Time{}
	}
	// Iterate real minutes so repeated and missing local times follow the zone's transitions.
	t := after.UTC().Truncate(time.Minute).Add(time.Minute)
	end := t.AddDate(8, 0, 0)
	for t.Before(end) {
		local := t.In(s.loc)
		f := s.fields
		day := f[2].values[local.Day()]
		weekday := f[4].values[int(local.Weekday())]
		matchDay := day && weekday
		if !f[2].wildcard && !f[4].wildcard {
			matchDay = day || weekday
		}
		if f[0].values[local.Minute()] && f[1].values[local.Hour()] && f[3].values[int(local.Month())] && matchDay {
			return t
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}
}

// NextN returns up to n occurrences strictly after after.
func (s Schedule) NextN(after time.Time, n int) []time.Time {
	var out []time.Time
	for len(out) < n {
		next := s.Next(after)
		if next.IsZero() {
			break
		}
		out = append(out, next)
		after = next
	}
	return out
}

// Words describes common schedules and preserves other expressions.
func (s Schedule) Words() string {
	if !s.at.IsZero() {
		return "once on " + s.at.In(s.loc).Format("2006-01-02 15:04 MST")
	}
	parts := strings.Fields(s.expr)
	if len(parts) == 5 && parts[2] == "*" && parts[3] == "*" {
		minute, e1 := strconv.Atoi(parts[0])
		hour, e2 := strconv.Atoi(parts[1])
		if e1 == nil && e2 == nil {
			if parts[4] == "*" {
				return fmt.Sprintf("every day at %02d:%02d", hour, minute)
			}
			if strings.EqualFold(parts[4], "mon-fri") || parts[4] == "1-5" {
				return fmt.Sprintf("weekdays at %02d:%02d", hour, minute)
			}
		}
	}
	return "cron " + s.expr
}
