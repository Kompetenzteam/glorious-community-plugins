// EU AI Act transparency notice (Art. 50, Regulation (EU) 2024/1689): this
// source file was authored with AI assistance (backend-developer agent) as
// part of the hello 1.0.0 reference-plugin phase A work and is subject to the
// repository's standard human code review before release.
package i18n

import (
	"os"
	"testing"
)

// TestParseLocale covers the locale normalisation rules: case-insensitive,
// whitespace-tolerant, and defaulting to German for anything unsupported.
func TestParseLocale(t *testing.T) {
	cases := []struct {
		in   string
		want Locale
	}{
		{"de", LocaleDE},
		{"DE", LocaleDE},
		{" de ", LocaleDE},
		{"en", LocaleEN},
		{"EN", LocaleEN},
		{"en-US", DefaultLocale},
		{"fr", DefaultLocale},
		{"", DefaultLocale},
		{"   ", DefaultLocale},
	}
	for _, tc := range cases {
		if got := ParseLocale(tc.in); got != tc.want {
			t.Errorf("ParseLocale(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestLookupDEAndEN asserts both locales render distinct, non-empty text and
// that the German and English strings actually differ for a representative key.
func TestLookupDEAndEN(t *testing.T) {
	de := Lookup(LocaleDE, KeyMissingCert)
	en := Lookup(LocaleEN, KeyMissingCert)
	if de == "" || en == "" {
		t.Fatalf("empty message: de=%q en=%q", de, en)
	}
	if de == en {
		t.Errorf("DE and EN messages should differ, both = %q", de)
	}
	if got := Lookup(LocaleDE, KeyIdentityMismatch); got == "" {
		t.Errorf("DE identity mismatch message is empty")
	}
	if got := Lookup(LocaleEN, KeyIdentityMismatch); got == "" {
		t.Errorf("EN identity mismatch message is empty")
	}
}

// TestMessagesCoverAllLocales guards the invariant that every key is
// translated in every supported locale — a missing entry would silently fall
// back to the bare key in production.
func TestMessagesCoverAllLocales(t *testing.T) {
	keys := Keys()
	if len(keys) == 0 {
		t.Fatal("no message keys registered")
	}
	for _, loc := range Locales() {
		table, ok := messages[loc]
		if !ok {
			t.Fatalf("no table for locale %q", loc)
		}
		for _, key := range keys {
			text, ok := table[key]
			if !ok {
				t.Errorf("locale %q missing key %q", loc, key)
				continue
			}
			if text == "" {
				t.Errorf("locale %q has empty text for key %q", loc, key)
			}
		}
	}
}

// TestUnknownKeyReturnsKeyNotPanic ensures a forgotten key is visible rather
// than empty or a panic.
func TestUnknownKeyReturnsKeyNotPanic(t *testing.T) {
	const missing Key = "does.not.exist"
	if got := Lookup(LocaleDE, missing); got != string(missing) {
		t.Errorf("Lookup(unknown) = %q, want %q", got, missing)
	}
}

// TestMessageUsesEnvironment checks that Message honours GLORIOUS_PLUGIN_LOCALE
// when no test override is set.
func TestMessageUsesEnvironment(t *testing.T) {
	SetLocale(nil)
	old, had := os.LookupEnv(EnvLocale)
	t.Cleanup(func() {
		if had {
			os.Setenv(EnvLocale, old)
		} else {
			os.Unsetenv(EnvLocale)
		}
	})

	os.Setenv(EnvLocale, "en")
	if got, want := Message(KeyMissingCert), Lookup(LocaleEN, KeyMissingCert); got != want {
		t.Errorf("Message with EN env = %q, want %q", got, want)
	}

	os.Setenv(EnvLocale, "de")
	if got, want := Message(KeyMissingCert), Lookup(LocaleDE, KeyMissingCert); got != want {
		t.Errorf("Message with DE env = %q, want %q", got, want)
	}

	// An unsupported value falls back to the default locale.
	os.Setenv(EnvLocale, "xx")
	if got, want := Message(KeyMissingCert), Lookup(DefaultLocale, KeyMissingCert); got != want {
		t.Errorf("Message with unknown env = %q, want default %q", got, want)
	}
}

// TestSetLocaleOverride checks the pinning helper used by tests and embedders.
func TestSetLocaleOverride(t *testing.T) {
	t.Cleanup(func() { SetLocale(nil) })
	en := LocaleEN
	SetLocale(&en)
	if got, want := Message(KeyMissingCert), Lookup(LocaleEN, KeyMissingCert); got != want {
		t.Errorf("Message with override = %q, want %q", got, want)
	}
}
