package scheduleservice

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCronDSTSkipsNonexistentWallTime(t *testing.T) {
	cron, err := ParseCron("30 2 * * *")
	if err != nil {
		t.Fatal(err)
	}
	loc, _ := time.LoadLocation("America/New_York")
	next, err := cron.Next(time.Date(2024, 3, 10, 1, 59, 0, 0, loc), loc)
	if err != nil {
		t.Fatal(err)
	}
	if next.In(loc).Day() != 11 || next.In(loc).Hour() != 2 || next.In(loc).Minute() != 30 {
		t.Fatalf("next=%v, want next valid 02:30 local day", next.In(loc))
	}
}

func TestCronRejectsInvalidTimezoneAndFields(t *testing.T) {
	if _, err := ParseCron("60 * * * *"); err == nil {
		t.Fatal("expected invalid minute")
	}
	if _, err := time.LoadLocation("Not/IANA"); err == nil {
		t.Fatal("expected invalid timezone")
	}
}

func TestCronUsesTraditionalDayOfMonthOrDayOfWeekSemantics(t *testing.T) {
	cron, err := ParseCron("0 9 15 * 1")
	if err != nil {
		t.Fatal(err)
	}
	// 2025-01-06 is a Monday but not the fifteenth.
	next, err := cron.Next(time.Date(2025, 1, 6, 8, 59, 0, 0, time.UTC), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC); !next.Equal(want) {
		t.Fatalf("next=%v, want %v", next, want)
	}
}

func TestCronRunsAmbiguousFallBackWallMinuteOnce(t *testing.T) {
	cron, err := ParseCron("30 1 * * *")
	if err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	first, err := cron.Next(time.Date(2024, 11, 3, 0, 0, 0, 0, loc), loc)
	if err != nil {
		t.Fatal(err)
	}
	second, err := cron.Next(first, loc)
	if err != nil {
		t.Fatal(err)
	}
	if first.In(loc).Day() != 3 || second.In(loc).Day() != 4 {
		t.Fatalf("ambiguous minute ran twice: first=%v second=%v", first.In(loc), second.In(loc))
	}
}

func sortedValues(f field) []int {
	values := make([]int, 0, len(f.values))
	for value := f.min; value <= f.max; value++ {
		if f.values[value] {
			values = append(values, value)
		}
	}
	return values
}

func TestParseCronStepSemantics(t *testing.T) {
	for _, tc := range []struct {
		expression string
		field      int
		want       []int
	}{
		// "N/S" runs from N to the field maximum (Vixie/ISC cron).
		{"5/15 * * * *", 0, []int{5, 20, 35, 50}},
		{"0/20 * * * *", 0, []int{0, 20, 40}},
		{"59/1 * * * *", 0, []int{59}},
		{"007/020 * * * *", 0, []int{7, 27, 47}},
		{"0 3/8 * * *", 1, []int{3, 11, 19}},
		{"0 0 2/10 * *", 2, []int{2, 12, 22}},
		{"0 0 31/1 * *", 2, []int{31}},
		{"0 0 1 1/3 *", 3, []int{1, 4, 7, 10}},
		{"0 0 * * 1/2", 4, []int{1, 3, 5}},
		{"0 0 * * 6/1", 4, []int{6}},
		// "*/S" covers the field and "N-M/S" stays inside the range.
		{"*/15 * * * *", 0, []int{0, 15, 30, 45}},
		{"10-30/7 * * * *", 0, []int{10, 17, 24}},
		{"5-5/3 * * * *", 0, []int{5}},
		{"0 */5 * * *", 1, []int{0, 5, 10, 15, 20}},
		// A step larger than the span yields only the start, however large.
		{"5/60 * * * *", 0, []int{5}},
		{"0 0 * * 0/7", 4, []int{0}},
		{"*/99999999999999999999 * * * *", 0, []int{0}},
		{"5/99999999999999999999 * * * *", 0, []int{5}},
		// Lists combine items.
		{"1,5/20,58 * * * *", 0, []int{1, 5, 25, 45, 58}},
		{"0 0 * * 0,3/2", 4, []int{0, 3, 5}},
	} {
		t.Run(tc.expression, func(t *testing.T) {
			cron, err := ParseCron(tc.expression)
			if err != nil {
				t.Fatal(err)
			}
			if got := sortedValues(cron.fields[tc.field]); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("field %d values=%v, want %v", tc.field+1, got, tc.want)
			}
		})
	}
}

func TestParseCronRejectsInvalidSteps(t *testing.T) {
	for _, expression := range []string{
		"5/0 * * * *", "*/0 * * * *", "5/ * * * *", "/5 * * * *", "5,/15 * * * *",
		"5//2 * * * *", "5/2/3 * * * *", "5/1.5 * * * *", "5/1e2 * * * *",
		// Numbers are unsigned decimal digits.
		"5/-1 * * * *", "5/+1 * * * *", "*/+2 * * * *", "+5 * * * *", "+5/15 * * * *",
		"5-+10 * * * *", "a/2 * * * *", "5-/2 * * * *", "\u0665/15 * * * *",
		// The start of "N/S" and both ends of "N-M/S" stay within the field.
		"60/5 * * * *", "1-60/2 * * * *", "10-5/2 * * * *", "0 24/1 * * *",
		"0 0 0/1 * *", "0 0 32/1 * *", "0 0 1 0/2 *", "0 0 1 13/1 *", "0 0 * * 7/1",
	} {
		if _, err := ParseCron(expression); !errors.Is(err, ErrInvalidCron) {
			t.Errorf("%q: error=%v, want ErrInvalidCron", expression, err)
		}
	}
	// The console translates errors by field number.
	if _, err := ParseCron("0 0 * * 7/1"); err == nil || !strings.Contains(err.Error(), "cron field 5") {
		t.Fatalf("error=%v, want it to name field 5", err)
	}
}

func TestCronNextWithSteps(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	utc := func(value string) time.Time {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	for _, tc := range []struct {
		expression string
		location   *time.Location
		after      time.Time
		want       time.Time
	}{
		{"5/15 * * * *", time.UTC, utc("2025-01-01T00:06:00Z"), utc("2025-01-01T00:20:00Z")},
		{"5/15 * * * *", time.UTC, utc("2025-01-01T00:50:00Z"), utc("2025-01-01T01:05:00Z")},
		{"10-30/7 * * * *", time.UTC, utc("2025-01-01T00:24:00Z"), utc("2025-01-01T01:10:00Z")},
		{"5/60 * * * *", time.UTC, utc("2025-01-01T00:05:00Z"), utc("2025-01-01T01:05:00Z")},
		{"0 3/8 * * *", time.UTC, utc("2025-01-01T04:00:00Z"), utc("2025-01-01T11:00:00Z")},
		{"0 0 2/10 * *", time.UTC, utc("2025-01-03T00:00:00Z"), utc("2025-01-12T00:00:00Z")},
		{"0 0 1 2/5 *", time.UTC, utc("2025-02-02T00:00:00Z"), utc("2025-07-01T00:00:00Z")},
		{"0 0 1 11/2 *", time.UTC, utc("2025-12-01T00:00:00Z"), utc("2026-11-01T00:00:00Z")},
		// 2025-01-07 is a Tuesday: "1/2" is Monday, Wednesday and Friday.
		{"0 9 * * 1/2", time.UTC, utc("2025-01-07T10:00:00Z"), utc("2025-01-08T09:00:00Z")},
		{"0 9 * * 5/2", time.UTC, utc("2025-01-07T10:00:00Z"), utc("2025-01-10T09:00:00Z")},
		// Restricted day-of-month and day-of-week stay alternatives: the
		// 20th/30th or a Sunday, whichever comes first.
		{"0 9 20/10 * 0/7", time.UTC, utc("2025-01-06T00:00:00Z"), utc("2025-01-12T09:00:00Z")},
		// 02:30 does not exist on 2024-03-10 in New York; 14:30 EDT is next.
		{"30 2/12 * * *", newYork, utc("2024-03-10T06:00:00Z"), utc("2024-03-10T18:30:00Z")},
		{"0 1/12 * * *", shanghai, utc("2024-12-31T16:00:00Z"), utc("2024-12-31T17:00:00Z")},
	} {
		t.Run(tc.expression+" "+tc.location.String(), func(t *testing.T) {
			cron, err := ParseCron(tc.expression)
			if err != nil {
				t.Fatal(err)
			}
			next, err := cron.Next(tc.after, tc.location)
			if err != nil {
				t.Fatal(err)
			}
			if !next.Equal(tc.want) {
				t.Fatalf("next=%v (%v local), want %v", next, next.In(tc.location), tc.want)
			}
		})
	}
}

func TestParseCronStepKeepsDayFieldsRestricted(t *testing.T) {
	// "*/2" restricts day-of-month, so it combines with Monday by OR: the
	// odd 3rd comes before Monday the 6th (AND would wait for the 13th).
	cron, err := ParseCron("0 9 */2 * 1")
	if err != nil {
		t.Fatal(err)
	}
	if cron.fields[2].unrestricted {
		t.Fatal("*/2 must not count as an unrestricted day-of-month")
	}
	next, err := cron.Next(time.Date(2025, 1, 1, 9, 0, 0, 0, time.UTC), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2025, 1, 3, 9, 0, 0, 0, time.UTC); !next.Equal(want) {
		t.Fatalf("next=%v, want %v", next, want)
	}
	// A bare "*" leaves day-of-month unrestricted: Mondays only.
	mondays, err := ParseCron("0 9 * * 1")
	if err != nil {
		t.Fatal(err)
	}
	next, err = mondays.Next(time.Date(2025, 1, 1, 9, 0, 0, 0, time.UTC), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2025, 1, 6, 9, 0, 0, 0, time.UTC); !next.Equal(want) {
		t.Fatalf("next=%v, want %v", next, want)
	}
}

func TestNumericStartSteps(t *testing.T) {
	for _, tc := range []struct {
		expression string
		want       []StepChange
	}{
		{"5/15 */10 1-10/2 * 1/2", []StepChange{
			{Field: 1, FieldName: "minute", Item: "5/15", Start: 5, Expanded: "5-59/15", Changed: true},
			{Field: 5, FieldName: "day-of-week", Item: "1/2", Start: 1, Expanded: "1-6/2", Changed: true},
		}},
		{"0,5/30 9 * * *", []StepChange{{Field: 1, FieldName: "minute", Item: "5/30", Start: 5, Expanded: "5-59/30", Changed: true}}},
		// The step exceeds the rest of the field: still only minute 30.
		{"30/45 * * * *", []StepChange{{Field: 1, FieldName: "minute", Item: "30/45", Start: 30, Expanded: "30-59/45", Changed: false}}},
		{"0 0 1 11/2 *", []StepChange{{Field: 4, FieldName: "month", Item: "11/2", Start: 11, Expanded: "11-12/2", Changed: false}}},
		{"*/5 * * * *", nil},
		{"0 9 * * 1-5", nil},
		{"61/5 * * * *", nil},
		{"* * *", nil},
	} {
		if got := NumericStartSteps(tc.expression); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: steps=%+v, want %+v", tc.expression, got, tc.want)
		}
	}
}
