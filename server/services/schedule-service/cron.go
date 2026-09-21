package scheduleservice

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Cron is the intentionally small, standard five-field cron dialect used by
// schedules. Fields are minute, hour, day-of-month, month, day-of-week.
type Cron struct{ fields [5]field }
type field struct {
	values       map[int]bool
	min, max     int
	unrestricted bool
}

func ParseCron(expression string) (Cron, error) {
	parts := strings.Fields(strings.TrimSpace(expression))
	if len(parts) != 5 {
		return Cron{}, errors.New("cron expression must contain five fields")
	}
	ranges := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
	var result Cron
	for i, part := range parts {
		f, err := parseField(part, ranges[i][0], ranges[i][1])
		if err != nil {
			return Cron{}, fmt.Errorf("cron field %d: %w", i+1, err)
		}
		result.fields[i] = f
	}
	return result, nil
}

func parseField(raw string, min, max int) (field, error) {
	f := field{values: map[int]bool{}, min: min, max: max, unrestricted: strings.TrimSpace(raw) == "*"}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			return field{}, errors.New("empty item")
		}
		base, step := item, 1
		if strings.Contains(item, "/") {
			parts := strings.Split(item, "/")
			if len(parts) != 2 {
				return field{}, errors.New("invalid step")
			}
			base = parts[0]
			parsed, err := strconv.Atoi(parts[1])
			if err != nil || parsed <= 0 {
				return field{}, errors.New("step must be positive")
			}
			step = parsed
		}
		start, end := min, max
		if base != "*" {
			if strings.Contains(base, "-") {
				parts := strings.Split(base, "-")
				if len(parts) != 2 {
					return field{}, errors.New("invalid range")
				}
				var err error
				start, err = strconv.Atoi(parts[0])
				if err != nil {
					return field{}, errors.New("invalid range")
				}
				end, err = strconv.Atoi(parts[1])
				if err != nil {
					return field{}, errors.New("invalid range")
				}
			} else {
				var err error
				start, err = strconv.Atoi(base)
				if err != nil {
					return field{}, errors.New("invalid value")
				}
				end = start
			}
		}
		if start < min || end > max || start > end {
			return field{}, fmt.Errorf("value must be between %d and %d", min, max)
		}
		for value := start; value <= end; value += step {
			f.values[value] = true
		}
	}
	if len(f.values) == 0 {
		return field{}, errors.New("field has no values")
	}
	return f, nil
}

func (c Cron) matches(t time.Time) bool {
	if !c.fields[0].values[t.Minute()] || !c.fields[1].values[t.Hour()] || !c.fields[3].values[int(t.Month())] {
		return false
	}
	dayOfMonth := c.fields[2].values[t.Day()]
	dayOfWeek := c.fields[4].values[int(t.Weekday())]
	if !c.fields[2].unrestricted && !c.fields[4].unrestricted {
		// Traditional five-field cron treats restricted day-of-month and
		// day-of-week fields as alternatives, rather than requiring both.
		return dayOfMonth || dayOfWeek
	}
	return dayOfMonth && dayOfWeek
}

// Next scans UTC minutes, converting each candidate into the requested IANA
// location. This skips nonexistent spring-forward wall times and is stable
// across repeated fall-back wall times without relying on process TZ state.
func (c Cron) Next(after time.Time, location *time.Location) (time.Time, error) {
	if location == nil {
		return time.Time{}, errors.New("timezone is required")
	}
	cursor := after.UTC().Truncate(time.Minute).Add(time.Minute)
	limit := cursor.Add(366 * 24 * time.Hour * 5)
	for cursor.Before(limit) {
		if c.matches(cursor.In(location)) && firstWallClockOccurrence(cursor, location) {
			return cursor, nil
		}
		cursor = cursor.Add(time.Minute)
	}
	return time.Time{}, errors.New("cron expression has no occurrence in search window")
}

// During a fall-back transition one wall-clock minute can map to two UTC
// instants. Running account automation twice is the more dangerous default,
// so schedules use only the first occurrence of an ambiguous local minute.
func firstWallClockOccurrence(candidate time.Time, location *time.Location) bool {
	local := candidate.In(location)
	for minutes := 1; minutes <= 180; minutes++ {
		previous := candidate.Add(-time.Duration(minutes) * time.Minute).In(location)
		if previous.Year() == local.Year() && previous.Month() == local.Month() && previous.Day() == local.Day() &&
			previous.Hour() == local.Hour() && previous.Minute() == local.Minute() {
			return false
		}
	}
	return true
}
