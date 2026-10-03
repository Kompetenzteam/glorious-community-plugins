// EU AI Act transparency notice (Art. 50, Regulation (EU) 2024/1689): this
// test file was authored with AI assistance (devops-engineer agent) as part of
// the community release-pipeline fix and is subject to the repository's
// standard human code review before release.
//
// Tests for verify-zip: a well-formed archive must expose its manifest
// entrypoint as a bundled member and carry a signature that verifies against
// the public half of the signing key.
package main

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
)

// writeArchive builds a minimal .glorious-plugin containing manifest.json plus
// the named binary, signing the canonical manifest with priv. The canonical
// form must match verify-zip exactly: json.Marshal of this struct with the
// signature field emptied (field order is the struct order, as in build-plugin).
func writeArchive(t *testing.T, path, binaryName, entrypoint string, priv ed25519.PrivateKey, tamper bool) {
	t.Helper()

	m := manifest{
		Name:       "hello",
		Version:    "1.0.0",
		Entrypoint: entrypoint,
	}
	canonical, err := json.Marshal(m) // Signature is "" here.
	if err != nil {
		t.Fatal(err)
	}
	m.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, canonical))
	manifestBytes, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, content := range map[string][]byte{
		"manifest.json":  manifestBytes,
		"functions.json": []byte(`{"functions":[]}`),
		"README.md":      []byte("# hello"),
		binaryName:       []byte("BINARY"),
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if tamper {
		// Re-open and rewrite the manifest with a garbage signature.
		_ = zw.Close()
		_ = f.Close()
		tamperArchive(t, path)
		return
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
}

// tamperArchive rewrites manifest.json inside an existing archive with a
// signature that cannot verify, to exercise the negative path.
func tamperArchive(t *testing.T, path string) {
	t.Helper()
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string][]byte{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		buf := make([]byte, f.UncompressedSize64)
		_, _ = rc.Read(buf)
		rc.Close()
		entries[f.Name] = buf
	}
	_ = zr.Close()

	var m map[string]any
	_ = json.Unmarshal(entries["manifest.json"], &m)
	m["signature"] = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	entries["manifest.json"], _ = json.Marshal(m)

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for name, content := range entries {
		w, _ := zw.Create(name)
		_, _ = w.Write(content)
	}
	_ = zw.Close()
}

func keyPair(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	dir := t.TempDir()
	path := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	return priv, path
}

func TestVerifyGoodArchive(t *testing.T) {
	priv, keyPath := keyPair(t)
	arch := filepath.Join(t.TempDir(), "hello-linux-amd64.glorious-plugin")
	writeArchive(t, arch, "hello", "./hello", priv, false)

	rc := runVerify(t, arch, keyPath)
	if rc != 0 {
		t.Fatalf("expected exit 0 for valid archive, got %d", rc)
	}
}

func TestVerifyRejectsTamperedSignature(t *testing.T) {
	priv, keyPath := keyPair(t)
	arch := filepath.Join(t.TempDir(), "hello-linux-amd64.glorious-plugin")
	writeArchive(t, arch, "hello", "./hello", priv, true)

	if rc := runVerify(t, arch, keyPath); rc == 0 {
		t.Fatal("expected non-zero exit for tampered signature")
	}
}

func TestVerifyRejectsMissingEntrypointBinary(t *testing.T) {
	priv, keyPath := keyPair(t)
	arch := filepath.Join(t.TempDir(), "hello-linux-amd64.glorious-plugin")
	// Manifest points at ./missing but the archive bundles ./hello.
	writeArchive(t, arch, "hello", "./missing", priv, false)

	if rc := runVerify(t, arch, keyPath); rc == 0 {
		t.Fatal("expected non-zero exit when entrypoint binary is absent")
	}
}

// runVerify invokes the package's main via the same code path used in CI by
// calling the verifier directly, keeping the test fast and dependency-free.
func runVerify(t *testing.T, arch, keyPath string) int {
	t.Helper()
	args := []string{arch, keyPath}
	return verifyMain(args)
}
