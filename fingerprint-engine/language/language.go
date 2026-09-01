package language

// Fingerprint stores browser language configuration.
type Fingerprint struct {
	Locale string
	Languages []string
}

func New(locale string, languages []string) Fingerprint {
	return Fingerprint{
		Locale: locale,
		Languages: languages,
	}
}
