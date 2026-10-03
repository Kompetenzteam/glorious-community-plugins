// EU AI Act transparency notice (Art. 50, Regulation (EU) 2024/1689): this
// source file was authored with AI assistance (backend-developer agent) as
// part of the hello 1.0.0 reference-plugin phase A work and is subject to the
// repository's standard human code review before release.
//
// Package i18n is the plugin's dependency-free, stdlib-only message lookup.
// Plugin messages that leave the process (log lines, handshake error strings,
// contract error text) are rendered in German or English. The active locale is
// read once from GLORIOUS_PLUGIN_LOCALE; the default is "de", matching the
// repository convention. Unknown locales fall back to the default instead of
// failing, so a misconfigured host variable degrades to readable output rather
// than an unusable plugin.
//
// The lookup is intentionally small: a static table of message keys per
// locale. There is no formatting engine and no external dependency (no
// go-i18n); a plugin must stay buildable with the Go standard library alone.
package i18n

import (
	"os"
	"strings"
	"sync"
)

// EnvLocale is the environment variable that selects the message locale. The
// values "de" and "en" are supported; any other value (including unset) falls
// back to DefaultLocale.
const EnvLocale = "GLORIOUS_PLUGIN_LOCALE"

// DefaultLocale is used when GLORIOUS_PLUGIN_LOCALE is unset or names an
// unsupported locale.
const DefaultLocale = "de"

// Locale is a supported message locale.
type Locale string

// Supported locales.
const (
	LocaleDE Locale = "de"
	LocaleEN Locale = "en"
)

// Key identifies a message. Keys are stable identifiers, never user text, so a
// missing translation is a programming error that the zero value makes visible
// (see Message).
type Key string

// Message keys used across the plugin. Every key must have an entry in every
// supported locale; TestMessagesCoverAllLocales enforces that invariant.
const (
	// KeyMissingCert is logged when GLORIOUS_PLUGIN_CERT_PEM is absent/empty.
	KeyMissingCert Key = "env.missing_cert"
	// KeyMissingKey is logged when GLORIOUS_PLUGIN_KEY_PEM is absent/empty.
	KeyMissingKey Key = "env.missing_key"
	// KeyMissingCA is logged when GLORIOUS_PLUGIN_CA_PEM is absent/empty or
	// not decodable.
	KeyMissingCA Key = "env.missing_ca"
	// KeyBadTLSMinVersion is logged for an unknown GLORIOUS_PLUGIN_TLS_MIN_VERSION.
	KeyBadTLSMinVersion Key = "env.bad_tls_min_version"
	// KeyBadKeypair is logged when the leaf cert/key pair is unusable.
	KeyBadKeypair Key = "env.bad_keypair"
	// KeyBadCAPool is logged when the CA PEM carries no usable certificate.
	KeyBadCAPool Key = "env.bad_ca_pool"
	// KeyIdentityMismatch is the message accompanying E_IDENTITY_MISMATCH.
	KeyIdentityMismatch Key = "handshake.identity_mismatch"
	// KeyShutdown is logged when a shutdown signal is received.
	KeyShutdown Key = "lifecycle.shutdown"
	// KeyListenerRejected is logged when the announced address is not loopback
	// or carries an out-of-range port.
	KeyListenerRejected Key = "lifecycle.listener_rejected"
)

// messages holds the static table: locale -> key -> text. It is populated in
// init and never mutated afterwards, so concurrent reads are safe.
var messages = map[Locale]map[Key]string{
	LocaleDE: {
		KeyMissingCert:      "GLORIOUS_PLUGIN_CERT_PEM fehlt oder ist leer",
		KeyMissingKey:       "GLORIOUS_PLUGIN_KEY_PEM fehlt oder ist leer",
		KeyMissingCA:        "GLORIOUS_PLUGIN_CA_PEM fehlt, ist leer oder kein base64-PEM",
		KeyBadTLSMinVersion: "GLORIOUS_PLUGIN_TLS_MIN_VERSION unbekannt (erlaubt: 1.2, 1.3)",
		KeyBadKeypair:       "Zertifikat/Schluessel-Paar ist unbrauchbar",
		KeyBadCAPool:        "GLORIOUS_PLUGIN_CA_PEM enthaelt kein lesbares Zertifikat",
		KeyIdentityMismatch: "Identitaet stimmt nicht mit dem erwarteten Plugin ueberein",
		KeyShutdown:         "Shutdown-Signal empfangen, beende",
		KeyListenerRejected: "Listener-Adresse wird vom Host nicht akzeptiert",
	},
	LocaleEN: {
		KeyMissingCert:      "GLORIOUS_PLUGIN_CERT_PEM is missing or empty",
		KeyMissingKey:       "GLORIOUS_PLUGIN_KEY_PEM is missing or empty",
		KeyMissingCA:        "GLORIOUS_PLUGIN_CA_PEM is missing, empty or not base64 PEM",
		KeyBadTLSMinVersion: "GLORIOUS_PLUGIN_TLS_MIN_VERSION is unknown (allowed: 1.2, 1.3)",
		KeyBadKeypair:       "certificate/key pair is unusable",
		KeyBadCAPool:        "GLORIOUS_PLUGIN_CA_PEM contains no readable certificate",
		KeyIdentityMismatch: "identity does not match the expected plugin",
		KeyShutdown:         "shutdown signal received, stopping",
		KeyListenerRejected: "listener address is not accepted by the host",
	},
}

// envLocale, when non-nil, overrides the environment lookup. Tests set it to
// pin a locale without touching the process environment; production leaves it
// nil and the lookup reads GLORIOUS_PLUGIN_LOCALE.
var (
	mu        sync.RWMutex
	envLocale *Locale
)

// ParseLocale normalises raw into a supported Locale, returning DefaultLocale
// for an empty or unsupported value. Matching is case-insensitive and tolerant
// of surrounding whitespace (host variables are not always tidy).
func ParseLocale(raw string) Locale {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case string(LocaleDE):
		return LocaleDE
	case string(LocaleEN):
		return LocaleEN
	default:
		return DefaultLocale
	}
}

// current returns the active locale: the test override when set, otherwise
// GLORIOUS_PLUGIN_LOCALE parsed with ParseLocale.
func current() Locale {
	mu.RLock()
	override := envLocale
	mu.RUnlock()
	if override != nil {
		return *override
	}
	return ParseLocale(os.Getenv(EnvLocale))
}

// SetLocale pins the active locale, ignoring the process environment. It exists
// for tests and for embedders that want deterministic output; pass nil to
// restore environment-driven behaviour.
func SetLocale(loc *Locale) {
	mu.Lock()
	envLocale = loc
	mu.Unlock()
}

// Message returns the text for key in the active locale. A missing translation
// returns the key itself stringified — never an empty string — so a forgotten
// entry is visible in logs instead of silently vanishing.
func Message(key Key) string {
	loc := current()
	if table, ok := messages[loc]; ok {
		if text, ok := table[key]; ok {
			return text
		}
	}
	return string(key)
}

// Lookup is the locale-explicit form of Message: it returns the text for key in
// loc without consulting the process environment or the test override. It is
// the primitive Message builds on and the one tests assert against.
func Lookup(loc Locale, key Key) string {
	if table, ok := messages[loc]; ok {
		if text, ok := table[key]; ok {
			return text
		}
	}
	return string(key)
}

// Keys returns every known message key. Callers use it to assert translation
// coverage; the order is unspecified.
func Keys() []Key {
	seen := make(map[Key]struct{})
	var out []Key
	for _, table := range messages {
		for key := range table {
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, key)
		}
	}
	return out
}

// Locales returns the supported locales. Callers use it to assert that every
// key is translated in every locale.
func Locales() []Locale {
	return []Locale{LocaleDE, LocaleEN}
}
