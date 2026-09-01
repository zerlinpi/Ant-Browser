package timezone

// TimezoneFingerprint controls locale and timezone profile.
type TimezoneFingerprint struct {
	Timezone string
	Locale string
}

func Default() TimezoneFingerprint {
	return TimezoneFingerprint{
		Timezone: "America/New_York",
		Locale: "en-US",
	}
}
