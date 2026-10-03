// EU AI Act transparency notice (Art. 50, Regulation (EU) 2024/1689): this
// source file was authored with AI assistance (backend-developer agent) as
// part of the hello 1.0.0 reference-plugin phase A work and is subject to the
// repository's standard human code review before release.
package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"glorious-community/plugins/hello/internal/i18n"
)

// fakeEnv returns a lookup function backed by a map, isolating each test from
// the process environment.
func fakeEnv(vars map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := vars[key]
		return v, ok
	}
}

// makePKI builds a throwaway CA and a leaf cert/key pair as PEM, mirroring the
// shapes the host provisions.
func makePKI(t *testing.T) (caPEM, leafCertPEM, leafKeyPEM []byte) {
	t.Helper()

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("CA key: %v", err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "config-test-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("CA cert: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("CA parse: %v", err)
	}
	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("leaf key: %v", err)
	}
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "config-test-leaf"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("leaf cert: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	if err != nil {
		t.Fatalf("leaf key marshal: %v", err)
	}
	leafCertPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER})
	leafKeyPEM = pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	return caPEM, leafCertPEM, leafKeyPEM
}

// TestTLSFromEnvHappyPath builds a working config from base64 PEM variables.
func TestTLSFromEnvHappyPath(t *testing.T) {
	t.Cleanup(func() { SetLookupEnv(nil) })

	caPEM, certPEM, keyPEM := makePKI(t)
	SetLookupEnv(fakeEnv(map[string]string{
		EnvCertPEM: base64.StdEncoding.EncodeToString(certPEM),
		EnvKeyPEM:  base64.StdEncoding.EncodeToString(keyPEM),
		EnvCAPEM:   base64.StdEncoding.EncodeToString(caPEM),
	}))

	cfg, err := TLSFromEnv()
	if err != nil {
		t.Fatalf("TLSFromEnv: %v", err)
	}
	if len(cfg.Certificates) != 1 {
		t.Errorf("certificates = %d, want 1", len(cfg.Certificates))
	}
	if cfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("ClientAuth = %v, want RequireAndVerifyClientCert", cfg.ClientAuth)
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %d, want TLS 1.2", cfg.MinVersion)
	}
	if cfg.ClientCAs == nil {
		t.Error("ClientCAs pool is nil")
	}
}

// TestTLSFromEnvFailsClosed asserts that a missing certificate, key or CA is a
// hard error (never a defaulted or partial config).
func TestTLSFromEnvFailsClosed(t *testing.T) {
	t.Cleanup(func() { SetLookupEnv(nil) })

	caPEM, certPEM, keyPEM := makePKI(t)
	certB64 := base64.StdEncoding.EncodeToString(certPEM)
	keyB64 := base64.StdEncoding.EncodeToString(keyPEM)

	cases := []struct {
		name    string
		vars    map[string]string
		wantMsg i18n.Key
	}{
		{
			name:    "missing cert",
			vars:    map[string]string{EnvKeyPEM: keyB64, EnvCAPEM: base64.StdEncoding.EncodeToString(caPEM)},
			wantMsg: i18n.KeyMissingCert,
		},
		{
			name:    "empty cert",
			vars:    map[string]string{EnvCertPEM: "  ", EnvKeyPEM: keyB64, EnvCAPEM: base64.StdEncoding.EncodeToString(caPEM)},
			wantMsg: i18n.KeyMissingCert,
		},
		{
			name:    "missing key",
			vars:    map[string]string{EnvCertPEM: certB64, EnvCAPEM: base64.StdEncoding.EncodeToString(caPEM)},
			wantMsg: i18n.KeyMissingKey,
		},
		{
			name:    "missing ca",
			vars:    map[string]string{EnvCertPEM: certB64, EnvKeyPEM: keyB64},
			wantMsg: i18n.KeyMissingCA,
		},
		{
			name:    "empty ca",
			vars:    map[string]string{EnvCertPEM: certB64, EnvKeyPEM: keyB64, EnvCAPEM: ""},
			wantMsg: i18n.KeyMissingCA,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			SetLookupEnv(fakeEnv(tc.vars))
			cfg, err := TLSFromEnv()
			if err == nil {
				t.Fatalf("expected error for %s, got config %+v", tc.name, cfg)
			}
			if cfg != nil {
				t.Errorf("expected nil config on failure, got %+v", cfg)
			}
			// The error must carry the localized message for the failing var.
			if want := i18n.Message(tc.wantMsg); want != "" && !contains(err.Error(), want) {
				t.Errorf("error %q does not contain %q", err.Error(), want)
			}
		})
	}
}

// TestTLSFromEnvBadCA covers a decodable but certificate-less CA value.
func TestTLSFromEnvBadCA(t *testing.T) {
	t.Cleanup(func() { SetLookupEnv(nil) })

	caPEM, certPEM, keyPEM := makePKI(t)
	_ = caPEM
	SetLookupEnv(fakeEnv(map[string]string{
		EnvCertPEM: base64.StdEncoding.EncodeToString(certPEM),
		EnvKeyPEM:  base64.StdEncoding.EncodeToString(keyPEM),
		EnvCAPEM:   base64.StdEncoding.EncodeToString([]byte("not a pem")),
	}))
	if _, err := TLSFromEnv(); err == nil {
		t.Fatal("expected error for CA without a readable certificate")
	}
}

// TestTLSMinVersion covers the default and both explicit values plus the
// fail-closed unknown case.
func TestTLSMinVersion(t *testing.T) {
	t.Cleanup(func() { SetLookupEnv(nil) })

	cases := []struct {
		name    string
		val     string
		set     bool
		want    uint16
		wantErr bool
	}{
		{"unset defaults to 1.2", "", false, tls.VersionTLS12, false},
		{"empty defaults to 1.2", "", true, tls.VersionTLS12, false},
		{"1.2", "1.2", true, tls.VersionTLS12, false},
		{"1.3", "1.3", true, tls.VersionTLS13, false},
		{"unknown fails closed", "1.1", true, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vars := map[string]string{}
			if tc.set {
				vars[EnvTLSMinVersion] = tc.val
			}
			SetLookupEnv(fakeEnv(vars))
			got, err := tlsMinVersion()
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got version %d", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("tlsMinVersion = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestDecodeLeafValueForms checks the three accepted transports: a file path
// (Unix FD form), base64 PEM (Windows form) and plain PEM, plus the rejection
// of garbage.
func TestDecodeLeafValueForms(t *testing.T) {
	pemBytes := []byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n")

	// Base64 form round-trips to the original bytes.
	got, err := decodeLeafValue(base64.StdEncoding.EncodeToString(pemBytes))
	if err != nil {
		t.Fatalf("base64 form: %v", err)
	}
	if string(got) != string(pemBytes) {
		t.Errorf("base64 form mismatch: got %q", got)
	}

	// Plain PEM form is accepted (the returned bytes are the trimmed value).
	got, err = decodeLeafValue(string(pemBytes))
	if err != nil {
		t.Fatalf("plain PEM form: %v", err)
	}
	if string(got) != strings.TrimSpace(string(pemBytes)) {
		t.Errorf("plain PEM form mismatch: got %q", got)
	}

	// Path form reads the file. A native absolute path is recognised in the
	// path branch (Windows drive form here; the Unix /proc/self/fd form is the
	// same branch).
	dir := t.TempDir()
	path := dir + string(os.PathSeparator) + "leaf.pem"
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatalf("write temp pem: %v", err)
	}
	got, err = decodeLeafValue(path)
	if err != nil {
		t.Fatalf("path form: %v", err)
	}
	if string(got) != string(pemBytes) {
		t.Errorf("path form mismatch: got %q", got)
	}

	// Garbage fails closed.
	if _, err := decodeLeafValue("!!!not-base64-or-pem!!!"); err == nil {
		t.Error("expected error for garbage value")
	}
}

// TestLooksLikePath pins the classifier that routes a value to the file-read
// branch versus the base64 branch.
func TestLooksLikePath(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"/proc/self/fd/4", true},
		{"/abs/cert.pem", true},
		{"./rel.pem", true},
		{"../up.pem", true},
		{"C:\\certs\\leaf.pem", true},
		{"c:/certs/leaf.pem", true},
		{"LS0tLS1CRUdJTi...", false},
		{"-----BEGIN CERTIFICATE-----", false},
		{"", false},
		{"C:", false},
	}
	for _, tc := range cases {
		if got := looksLikePath(tc.in); got != tc.want {
			t.Errorf("looksLikePath(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestDecodeLeafValueMissingPath checks that a path that cannot be read is an
// error, not a silent empty value.
func TestDecodeLeafValueMissingPath(t *testing.T) {
	if _, err := decodeLeafValue("/nonexistent/does/not/exist.pem"); err == nil {
		t.Error("expected error for unreadable path")
	}
}

// contains reports whether haystack holds needle.
func contains(haystack, needle string) bool {
	return len(needle) == 0 || strings.Contains(haystack, needle)
}
