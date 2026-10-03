// EU AI Act transparency notice (Art. 50, Regulation (EU) 2024/1689): this
// source file was authored with AI assistance (backend-developer agent) as
// part of the hello 1.0.0 reference-plugin phase A work and is subject to the
// repository's standard human code review before release.
//
// Package config turns the host-provisioned environment into a ready
// *tls.Config for the plugin's mTLS listener. It is deliberately separate from
// main so the parsing rules are unit-testable without starting a process.
//
// Fail-closed rule: every value the handshake depends on (leaf certificate,
// leaf key, CA trust anchor) must be present and usable. There are no defaults
// for certificate material — a missing or malformed value is an error, never a
// silent downgrade. The only defaulted value is the TLS minimum version, which
// defaults to TLS 1.2 to match the host (mtls.go:324); an unknown explicit
// version is still an error rather than a silent fallback.
//
// Transport forms: the host chooses how it hands over leaf material. On
// Windows it passes base64-encoded PEM in the same variables; on Unix it
// passes file paths (/proc/self/fd/4 for the certificate, /proc/self/fd/3 for
// the key) that the plugin reads as files. Both forms are supported.
package config

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"strings"

	"glorious-community/plugins/hello/internal/i18n"
)

// Environment variable names, mirroring internal/plugins/runtime.go.
const (
	// EnvCertPEM carries the leaf certificate (path or base64 PEM).
	EnvCertPEM = "GLORIOUS_PLUGIN_CERT_PEM"
	// EnvKeyPEM carries the leaf private key (path or base64 PEM).
	EnvKeyPEM = "GLORIOUS_PLUGIN_KEY_PEM"
	// EnvCAPEM carries the CA trust anchor (base64 PEM).
	EnvCAPEM = "GLORIOUS_PLUGIN_CA_PEM"
	// EnvTLSMinVersion optionally raises the TLS minimum version ("1.2"/"1.3").
	EnvTLSMinVersion = "GLORIOUS_PLUGIN_TLS_MIN_VERSION"
)

// lookupEnv is the indirection tests replace to feed a fixed environment. In
// production it is os.LookupEnv; a lookup returning ok=false means "unset".
var lookupEnv = os.LookupEnv

// TLSFromEnv builds the server-side mTLS configuration from the host
// environment. It returns an error (never a partial config) when certificate
// material is missing or unusable.
func TLSFromEnv() (*tls.Config, error) {
	certPEM, err := leafMaterial(EnvCertPEM, i18n.KeyMissingCert)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", EnvCertPEM, err)
	}
	keyPEM, err := leafMaterial(EnvKeyPEM, i18n.KeyMissingKey)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", EnvKeyPEM, err)
	}
	caPEM, err := caMaterial()
	if err != nil {
		return nil, err
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", i18n.Message(i18n.KeyBadKeypair), err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New(i18n.Message(i18n.KeyBadCAPool))
	}

	minVersion, err := tlsMinVersion()
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		MinVersion:   minVersion,
	}, nil
}

// leafMaterial reads leaf material from the transport the host chose. The value
// is either a path (Unix: /proc/self/fd/3 or /proc/self/fd/4; read as a file)
// or base64 PEM (Windows). Both are accepted; an unset or empty variable is an
// error keyed by missingKey for a localized message.
func leafMaterial(envKey string, missingKey i18n.Key) ([]byte, error) {
	raw, ok := lookupEnv(envKey)
	if !ok || strings.TrimSpace(raw) == "" {
		return nil, errors.New(i18n.Message(missingKey))
	}
	return decodeLeafValue(raw)
}

// decodeLeafValue distinguishes the two transport forms: a value that looks
// like a file path (absolute Unix "/...", Windows drive "C:\..." or relative
// "./...") is read from disk (Unix FD form); otherwise it is decoded as base64
// PEM (Windows form). Plain PEM that is not base64 is also accepted, so a
// manually provisioned value does not fail the plugin.
func decodeLeafValue(raw string) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	if looksLikePath(trimmed) {
		data, err := os.ReadFile(trimmed)
		if err != nil {
			return nil, fmt.Errorf("read PEM file %q: %w", trimmed, err)
		}
		return data, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		// Some setups deliver plain PEM instead of base64 — accept it, but
		// only when it really is a PEM block.
		if block, _ := pem.Decode([]byte(trimmed)); block != nil {
			return []byte(trimmed), nil
		}
		return nil, fmt.Errorf("value is neither a file path nor base64 PEM: %w", err)
	}
	return decoded, nil
}

// looksLikePath reports whether value is a filesystem path rather than encoded
// PEM: it starts with "/" (absolute Unix, including the /proc/self/fd transport
// the host uses), "./" or ".." (relative), or has a Windows drive prefix
// ("C:\" / "C:/").
func looksLikePath(value string) bool {
	if value == "" {
		return false
	}
	if strings.HasPrefix(value, "/") || strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") {
		return true
	}
	if len(value) >= 3 && isDriveLetter(value[0]) && value[1] == ':' && (value[2] == '\\' || value[2] == '/') {
		return true
	}
	return false
}

// isDriveLetter reports whether b is an ASCII letter, the shape of a Windows
// drive designator.
func isDriveLetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// caMaterial reads the CA trust anchor from EnvCAPEM (base64 PEM). There is no
// default; a missing or undecodable value is an error.
func caMaterial() ([]byte, error) {
	raw, ok := lookupEnv(EnvCAPEM)
	if !ok || strings.TrimSpace(raw) == "" {
		return nil, errors.New(i18n.Message(i18n.KeyMissingCA))
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %w", EnvCAPEM, errors.New(i18n.Message(i18n.KeyMissingCA)), err)
	}
	return decoded, nil
}

// tlsMinVersion reads EnvTLSMinVersion ("1.2"/"1.3"). When unset it returns
// TLS 1.2, matching the host default (mtls.go:324). An unknown value is an
// error (fail-closed instead of a silent downgrade).
func tlsMinVersion() (uint16, error) {
	raw, ok := lookupEnv(EnvTLSMinVersion)
	if !ok || strings.TrimSpace(raw) == "" {
		return tls.VersionTLS12, nil
	}
	switch strings.TrimSpace(raw) {
	case "1.2":
		return tls.VersionTLS12, nil
	case "1.3":
		return tls.VersionTLS13, nil
	default:
		return 0, fmt.Errorf("%s: %q (%s)", EnvTLSMinVersion, raw, i18n.Message(i18n.KeyBadTLSMinVersion))
	}
}

// SetLookupEnv replaces the environment lookup used by this package. It exists
// for tests; passing nil restores os.LookupEnv.
func SetLookupEnv(fn func(string) (string, bool)) {
	if fn == nil {
		lookupEnv = os.LookupEnv
		return
	}
	lookupEnv = fn
}
