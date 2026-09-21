package scheduleservice

import (
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
