// Package scheduler provides a distributed cron and periodic job scheduler for Go microservices.
// It supports standard 5-field cron expressions, interval specifications, predefined macros,
// timezone localization, overlap prevention, and distributed coordination via distlock.
package scheduler

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrInvalidSpec is returned when a cron expression or interval spec cannot be parsed.
	ErrInvalidSpec = errors.New("scheduler: invalid schedule specification")
	// ErrJobNotFound is returned when operating on a job identifier that does not exist.
	ErrJobNotFound = errors.New("scheduler: job not found")
	// ErrSchedulerRunning is returned when attempting to start a scheduler that is already running.
	ErrSchedulerRunning = errors.New("scheduler: scheduler is already running")
	// ErrSchedulerStopped is returned when attempting to operate on a stopped scheduler.
	ErrSchedulerStopped = errors.New("scheduler: scheduler is stopped")
)

// Schedule defines the contract for computing successive job execution times.
type Schedule interface {
	// Next returns the next scheduled time strictly after the given reference time t.
	Next(t time.Time) time.Time
}

// EverySchedule represents an interval-based schedule (e.g. "@every 10s").
type EverySchedule struct {
	Interval time.Duration
}

// Every returns a Schedule that triggers periodically at the given duration interval.
func Every(interval time.Duration) Schedule {
	if interval <= 0 {
		interval = time.Second
	}
	return &EverySchedule{Interval: interval}
}

// Next returns t + Interval.
func (s *EverySchedule) Next(t time.Time) time.Time {
	return t.Add(s.Interval)
}

// CronSchedule implements Schedule for standard 5-field cron specifications.
// Fields: minute (0-59), hour (0-23), day of month (1-31), month (1-12), day of week (0-6, Sun=0 or 7).
type CronSchedule struct {
	Minutes  uint64 // bits 0-59
	Hours    uint64 // bits 0-23
	Days     uint64 // bits 1-31 (bit 0 unused)
	Months   uint64 // bits 1-12 (bit 0 unused)
	Weekdays uint64 // bits 0-6 (0=Sun, 6=Sat)
	Location *time.Location
}

// Next calculates the earliest time after t that satisfies the cron specification.
// Searches up to 5 years in advance; if no match is found, returns zero time.
func (s *CronSchedule) Next(t time.Time) time.Time {
	loc := s.Location
	if loc == nil {
		loc = time.UTC
	}

	t = t.In(loc).Truncate(time.Minute).Add(time.Minute)

	yearLimit := t.Year() + 5

	for t.Year() <= yearLimit {
		// Month match
		if (1<<t.Month())&s.Months == 0 {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc)
			continue
		}

		// Day of month & day of week match
		dayMatch := (1<<t.Day())&s.Days != 0
		weekdayMatch := (1<<t.Weekday())&s.Weekdays != 0

		if !dayMatch || !weekdayMatch {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
			continue
		}

		// Hour match
		if (1<<t.Hour())&s.Hours == 0 {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, loc)
			continue
		}

		// Minute match
		if (1<<t.Minute())&s.Minutes == 0 {
			t = t.Add(time.Minute)
			continue
		}

		return t
	}

	return time.Time{}
}

// Parse parses a schedule specification string into a Schedule.
// Supports:
//   - Standard 5-field cron: "0 0 * * *"
//   - Optional timezone prefix: "CRON_TZ=America/New_York 0 9 * * 1-5" or "TZ=UTC 0 12 * * *"
//   - Predefined macros: "@yearly", "@annually", "@monthly", "@weekly", "@daily", "@midnight", "@hourly"
//   - Intervals: "@every 5m", "@every 1h30m"
func Parse(spec string) (Schedule, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf("%w: empty expression", ErrInvalidSpec)
	}

	// Check for @every interval
	if strings.HasPrefix(spec, "@every ") {
		durationStr := strings.TrimSpace(spec[7:])
		d, err := time.ParseDuration(durationStr)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid interval duration %q: %v", ErrInvalidSpec, durationStr, err)
		}
		return Every(d), nil
	}

	loc := time.UTC

	// Check for timezone prefix: CRON_TZ=... or TZ=...
	if strings.HasPrefix(spec, "CRON_TZ=") || strings.HasPrefix(spec, "TZ=") {
		parts := strings.Fields(spec)
		if len(parts) < 2 {
			return nil, fmt.Errorf("%w: missing cron fields after timezone", ErrInvalidSpec)
		}
		tzPart := parts[0]
		var tzName string
		if strings.HasPrefix(tzPart, "CRON_TZ=") {
			tzName = tzPart[8:]
		} else {
			tzName = tzPart[3:]
		}
		var err error
		loc, err = time.LoadLocation(tzName)
		if err != nil {
			return nil, fmt.Errorf("%w: unknown timezone %q: %v", ErrInvalidSpec, tzName, err)
		}
		spec = strings.Join(parts[1:], " ")
	}

	// Predefined macros
	switch strings.ToLower(spec) {
	case "@yearly", "@annually":
		spec = "0 0 1 1 *"
	case "@monthly":
		spec = "0 0 1 * *"
	case "@weekly":
		spec = "0 0 * * 0"
	case "@daily", "@midnight":
		spec = "0 0 * * *"
	case "@hourly":
		spec = "0 * * * *"
	}

	fields := strings.Fields(spec)
	if len(fields) != 5 {
		return nil, fmt.Errorf("%w: expected 5 fields, got %d in %q", ErrInvalidSpec, len(fields), spec)
	}

	minutes, err := parseField(fields[0], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("%w: minute field: %v", ErrInvalidSpec, err)
	}

	hours, err := parseField(fields[1], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("%w: hour field: %v", ErrInvalidSpec, err)
	}

	days, err := parseField(fields[2], 1, 31)
	if err != nil {
		return nil, fmt.Errorf("%w: day-of-month field: %v", ErrInvalidSpec, err)
	}

	months, err := parseField(fields[3], 1, 12)
	if err != nil {
		return nil, fmt.Errorf("%w: month field: %v", ErrInvalidSpec, err)
	}

	dow, err := parseDayOfWeek(fields[4])
	if err != nil {
		return nil, fmt.Errorf("%w: day-of-week field: %v", ErrInvalidSpec, err)
	}

	return &CronSchedule{
		Minutes:  minutes,
		Hours:    hours,
		Days:     days,
		Months:   months,
		Weekdays: dow,
		Location: loc,
	}, nil
}

// parseField parses a single cron expression field (wildcard, range, list, step).
func parseField(field string, min, max int) (uint64, error) {
	var bits uint64

	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return 0, errors.New("empty part in list")
		}

		step := 1
		if strings.Contains(part, "/") {
			subParts := strings.Split(part, "/")
			if len(subParts) != 2 {
				return 0, fmt.Errorf("invalid step syntax in %q", part)
			}
			var err error
			step, err = strconv.Atoi(subParts[1])
			if err != nil || step <= 0 {
				return 0, fmt.Errorf("invalid step value in %q", part)
			}
			part = subParts[0]
		}

		start, end := min, max
		if part != "*" {
			if strings.Contains(part, "-") {
				subParts := strings.Split(part, "-")
				if len(subParts) != 2 {
					return 0, fmt.Errorf("invalid range syntax in %q", part)
				}
				var err error
				start, err = strconv.Atoi(subParts[0])
				if err != nil {
					return 0, fmt.Errorf("invalid range start in %q", part)
				}
				end, err = strconv.Atoi(subParts[1])
				if err != nil {
					return 0, fmt.Errorf("invalid range end in %q", part)
				}
			} else {
				val, err := strconv.Atoi(part)
				if err != nil {
					return 0, fmt.Errorf("invalid number in %q", part)
				}
				start, end = val, val
			}
		}

		if start < min || end > max || start > end {
			return 0, fmt.Errorf("value out of bounds [%d-%d] in %q", min, max, part)
		}

		for i := start; i <= end; i += step {
			bits |= 1 << uint(i)
		}
	}

	return bits, nil
}

// parseDayOfWeek parses day-of-week, normalizing 7 to 0 (Sunday).
func parseDayOfWeek(field string) (uint64, error) {
	// Support day names (Sun, Mon, Tue, etc.)
	names := map[string]string{
		"SUN": "0", "MON": "1", "TUE": "2", "WED": "3", "THU": "4", "FRI": "5", "SAT": "6",
	}

	upper := strings.ToUpper(field)
	for k, v := range names {
		upper = strings.ReplaceAll(upper, k, v)
	}

	bits, err := parseField(upper, 0, 7)
	if err != nil {
		return 0, err
	}

	// Normalize day 7 (Sunday) to day 0 (Sunday)
	if (bits & (1 << 7)) != 0 {
		bits |= (1 << 0)
		bits &^= (1 << 7)
	}

	return bits, nil
}
