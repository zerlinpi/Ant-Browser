package scheduleservice

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// ErrInvalidCron classifies every rejected cron expression: a malformed one
// and one that never fires (such as 30 February). Callers match it with
// errors.Is; the wrapped message names the problem.
var ErrInvalidCron = errors.New("invalid cron expression")

// Cron is the intentionally small, standard five-field cron dialect used by
// schedules. Fields are minute, hour, day-of-month, month, day-of-week.
//
// Each field is a comma-separated list of items. An item is "*", a number
// "N" or a range "N-M", optionally followed by a step "/S" with S >= 1.
// As in Vixie/ISC cron a step counts from the start of its item: "*/S"
// covers the whole field, "N-M/S" the range and "N/S" runs from N to the
// field maximum. A step larger than the span yields only the start. Numbers
// are unsigned decimal digits.
type Cron struct{ fields [5]field }
type field struct {
	values       map[int]bool
	min, max     int
	unrestricted bool
}

// cronRanges are the inclusive bounds of the five fields; day-of-week runs
// from 0 (Sunday) to 6.
var cronRanges = [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}

var cronFieldNames = [5]string{"minute", "hour", "day-of-month", "month", "day-of-week"}

func ParseCron(expression string) (Cron, error) {
	parts := strings.Fields(strings.TrimSpace(expression))
	if len(parts) != 5 {
		return Cron{}, fmt.Errorf("%w: cron expression must contain five fields", ErrInvalidCron)
	}
	var result Cron
	for i, part := range parts {
		f, err := parseField(part, cronRanges[i][0], cronRanges[i][1])
		if err != nil {
			return Cron{}, fmt.Errorf("%w: cron field %d: %v", ErrInvalidCron, i+1, err)
		}
		result.fields[i] = f
	}
	return result, nil
}

func parseField(raw string, min, max int) (field, error) {
	// Only a bare "*" leaves the field unrestricted; "*/S" restricts it,
	// which matters for the day-of-month/day-of-week combination.
	f := field{values: map[int]bool{}, min: min, max: max, unrestricted: strings.TrimSpace(raw) == "*"}
	for _, item := range strings.Split(raw, ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			return field{}, errors.New("empty item")
		}
		start, end, step, err := parseItem(item, min, max)
		if err != nil {
			return field{}, err
		}
		// Stopping once the remaining span is shorter than the step never
		// computes a value past end, so even a saturated step cannot overflow.
		for value := start; ; value += step {
			f.values[value] = true
			if end-value < step {
				break
			}
		}
	}
	if len(f.values) == 0 {
		return field{}, errors.New("field has no values")
	}
	return f, nil
}

// parseItem returns the first value, the inclusive bound and the step of one
// list item.
func parseItem(item string, min, max int) (start, end, step int, err error) {
	base, stepText, stepped := strings.Cut(item, "/")
	step = 1
	if stepped {
		if strings.Contains(stepText, "/") {
			return 0, 0, 0, errors.New("invalid step")
		}
		if step, err = parseStep(stepText); err != nil {
			return 0, 0, 0, err
		}
	}
	switch {
	case base == "*":
		start, end = min, max
	case strings.Contains(base, "-"):
		low, high, _ := strings.Cut(base, "-")
		var lowErr, highErr error
		start, lowErr = parseNumber(low)
		end, highErr = parseNumber(high)
		if lowErr != nil || highErr != nil {
			return 0, 0, 0, errors.New("invalid range")
		}
	default:
		if start, err = parseNumber(base); err != nil {
			return 0, 0, 0, errors.New("invalid value")
		}
		end = start
		if stepped {
			// "N/S" means "N-max/S".
			end = max
		}
	}
	if start < min || end > max || start > end {
		return 0, 0, 0, fmt.Errorf("value must be between %d and %d", min, max)
	}
	return start, end, step, nil
}

// parseNumber accepts unsigned decimal digits only. strconv.Atoi alone would
// also accept a sign such as "+5", which cron does not.
func parseNumber(text string) (int, error) {
	if !isDigits(text) {
		return 0, errors.New("not a number")
	}
	return strconv.Atoi(text)
}

// parseStep accepts any unsigned decimal step of at least 1. A step beyond
// the int range saturates: it exceeds every field span, so like any other
// step larger than the span it yields only the start.
func parseStep(text string) (int, error) {
	if !isDigits(text) {
		return 0, errors.New("step must be positive")
	}
	step, err := strconv.Atoi(text)
	if errors.Is(err, strconv.ErrRange) {
		step, err = math.MaxInt, nil
	}
	if err != nil || step < 1 {
		return 0, errors.New("step must be positive")
	}
	return step, nil
}

func isDigits(text string) bool {
	if text == "" {
		return false
	}
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
	}
	return true
}

// StepChange describes a list item written as "N/S". Before the Vixie/ISC
// step semantics such an item selected only N; it now selects N-max/S.
type StepChange struct {
	// Field is the 1-based field number and FieldName its name (minute,
	// hour, day-of-month, month or day-of-week).
	Field     int
	FieldName string
	// Item is the item as written and Start its N. Expanded is the explicit
	// range form, such as "5-59/15" for "5/15" in the minute field.
	Item     string
	Start    int
	Expanded string
	// Changed is false when the step exceeds the rest of the field, so the
	// item still selects only N.
	Changed bool
}

// NumericStartSteps lists the "N/S" items of an expression that ParseCron
// accepts, in field order. The migration preflight uses it to list
// schedules whose meaning changed with the step semantics.
func NumericStartSteps(expression string) []StepChange {
	if _, err := ParseCron(expression); err != nil {
		return nil
	}
	var changes []StepChange
	for index, part := range strings.Fields(expression) {
		for _, item := range strings.Split(part, ",") {
			base, stepText, stepped := strings.Cut(strings.TrimSpace(item), "/")
			if !stepped || !isDigits(base) {
				continue
			}
			start, _ := parseNumber(base)
			step, _ := parseStep(stepText)
			max := cronRanges[index][1]
			changes = append(changes, StepChange{
				Field: index + 1, FieldName: cronFieldNames[index],
				Item: item, Start: start, Expanded: fmt.Sprintf("%d-%d/%s", start, max, stepText),
				Changed: max-start >= step,
			})
		}
	}
	return changes
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
	return time.Time{}, fmt.Errorf("%w: cron expression has no occurrence in search window", ErrInvalidCron)
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
