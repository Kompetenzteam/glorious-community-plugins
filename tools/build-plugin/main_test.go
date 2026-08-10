package main

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- Fixtures ---------------------------------------------------------------

func validManifest() string {
	return `{
  "name": "wiki",
  "version": "1.2.0",
  "description": "Wiki-Modul für Glorious",
  "entrypoint": "./plugin",
  "permissions": ["network"],
  "license": "MIT",
  "signature": "b2xkZS1zaWduYXR1cmU="
}`
}

func validFunctions() string {
	return `{
  "version": "1.0.0",
  "objects": [
    {
      "name": "wiki",
      "description": "Wiki-Seiten verwalten",
      "actions": ["read", "create", "update", "delete"],
      "default_role_permissions": {
        "user": ["read", "create"],
        "admin": ["*"]
      }
    }
  ]
}`
}

// validReadme erfüllt die Mindestanforderungen: >= 300 Runes und beide
// Pflichtsektionen (case-insensitiv geprüft).
func validReadme() string {
	return `# Wiki-Plugin

## Beschreibung

Das Wiki-Plugin stellt ein Markdown-Wiki mit Seitenverwaltung, Volltextsuche
und Versionshistorie bereit. Es integriert sich in die Glorious Platform über
die Plugin-Schnittstelle und legt seine Daten im privaten Plugin-Verzeichnis
ab. Die Seiten werden als Markdown-Dateien gespeichert und bei Änderungen
sofort indexiert, sodass die Suche immer aktuell ist.

Die Bedienung erfolgt über das Wiki-Widget auf dem Dashboard sowie über die
HTMX-gestützten Seiten des Plugins. Mehrere Benutzer können gleichzeitig an
verschiedenen Seiten arbeiten; Konflikte werden über die Versionshistorie
aufgelöst.

## Berechtigungen

Das Plugin fordert ausschließlich Berechtigungen auf die eigenen Objekte an
(siehe functions.json): wiki:read zum Anzeigen von Seiten, wiki:create und
wiki:update zum Anlegen und Bearbeiten sowie wiki:delete zum Löschen. Es
greift nicht auf Benutzer-, Rollen- oder Systemdaten zu.
`
}

// writeFile schreibt eine Fixture-Datei in ein Temp-Verzeichnis.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// writeKeyPEM schreibt den privaten Key als PKCS#8-PEM und liefert ihn zurück.
func writeKeyPEM(t *testing.T, dir string) (ed25519.PrivateKey, string) {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	p := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	return priv, p
}

// buildArgs baut die CLI-Argumente für run().
func buildArgs(manifestPath, functionsPath, readmePath, binaryPath, outPath, keyPath string, extra ...string) []string {
	args := []string{
		"--manifest", manifestPath,
		"--functions", functionsPath,
		"--readme", readmePath,
		"--binary", binaryPath,
		"--out", outPath,
		"--signing-key", keyPath,
	}
	return append(args, extra...)
}

// verifyManifestSignature ist der eigene Verify des Tests: manifest.json aus
// dem ZIP lesen, Base64-Signatur decodieren, kanonisches JSON (signature
// geleert) nachbilden und mit ed25519.Verify gegen den Public-Key prüfen.
func verifyManifestSignature(t *testing.T, zipPath string, pub ed25519.PublicKey) *Manifest {
	t.Helper()
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer zr.Close()

	var m Manifest
	found := false
	for _, f := range zr.File {
		if f.Name != "manifest.json" {
			continue
		}
		found = true
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open manifest entry: %v", err)
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("read manifest entry: %v", err)
		}
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("unmarshal manifest: %v", err)
		}
	}
	if !found {
		t.Fatal("manifest.json fehlt im Archiv")
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil || len(sig) != ed25519.SignatureSize {
		t.Fatalf("Signatur nicht decodierbar: %v (len %d)", err, len(sig))
	}
	canonical, err := canonicalManifestJSON(&m)
	if err != nil {
		t.Fatalf("canonical manifest: %v", err)
	}
	if !ed25519.Verify(pub, canonical, sig) {
		t.Fatal("Signatur ist ungültig")
	}
	return &m
}

// --- Tests ------------------------------------------------------------------

// (a) Gültige Eingaben → ZIP entsteht, Signatur verifizierbar, Grenzwerte
// eingehalten.
func TestBuildValidArchive(t *testing.T) {
	dir := t.TempDir()
	priv, keyPath := writeKeyPEM(t, dir)

	manifestPath := writeFile(t, dir, "manifest.json", validManifest())
	functionsPath := writeFile(t, dir, "functions.json", validFunctions())
	readmePath := writeFile(t, dir, "README.md", validReadme())
	binaryPath := writeFile(t, dir, "plugin", "MZ fake binary")
	iconPath := writeFile(t, dir, "icon.svg", "<svg/>")
	assetDir := filepath.Join(dir, "assets")
	if err := os.MkdirAll(filepath.Join(assetDir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	writeFile(t, assetDir, "data.txt", "asset data")
	writeFile(t, filepath.Join(assetDir, "sub"), "nested.txt", "nested data")

	outPath := filepath.Join(dir, "wiki-1.2.0-linux-amd64.glorious-plugin")
	err := run(buildArgs(manifestPath, functionsPath, readmePath, binaryPath, outPath, keyPath,
		"--icon", iconPath, "--asset", assetDir))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("ZIP entstand nicht: %v", err)
	}

	// Signatur verifizieren (eigener Verify, s. o.).
	pub, _ := priv.Public().(ed25519.PublicKey)
	m := verifyManifestSignature(t, outPath, pub)
	if m.Name != "wiki" || m.Version != "1.2.0" {
		t.Fatalf("Manifestinhalt falsch: %+v", m)
	}

	// Struktur des Archivs prüfen.
	zr, err := zip.OpenReader(outPath)
	if err != nil {
		t.Fatalf("open archive: %v", err)
	}
	defer zr.Close()
	want := map[string]bool{
		"manifest.json":         false,
		"functions.json":        false,
		"README.md":             false,
		"plugin":                false,
		"assets/icon.svg":       false,
		"assets/data.txt":       false,
		"assets/sub/nested.txt": false,
	}
	var total uint64
	for _, f := range zr.File {
		if _, ok := want[f.Name]; ok {
			want[f.Name] = true
		}
		total += f.UncompressedSize64
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("Eintrag %q fehlt im Archiv", name)
		}
	}
	// Binary ist ausführbar markiert.
	for _, f := range zr.File {
		if f.Name == "plugin" && f.Mode()&0o111 == 0 {
			t.Errorf("Binary-Eintrag ist nicht ausführbar (mode %v)", f.Mode())
		}
	}

	// (e) Grenzwerte: Einträge <= 1000, Gesamtgröße <= 50 MiB.
	if len(zr.File) > maxArchiveEntries {
		t.Errorf("%d Einträge überschreiten das Limit %d", len(zr.File), maxArchiveEntries)
	}
	if total > maxArchiveSize {
		t.Errorf("unkomprimierter Inhalt %d Bytes überschreitet %d", total, maxArchiveSize)
	}
}

// (b) README fehlt → Fehler.
func TestBuildReadmeMissing(t *testing.T) {
	dir := t.TempDir()
	_, keyPath := writeKeyPEM(t, dir)

	manifestPath := writeFile(t, dir, "manifest.json", validManifest())
	functionsPath := writeFile(t, dir, "functions.json", validFunctions())
	binaryPath := writeFile(t, dir, "plugin", "MZ fake binary")
	outPath := filepath.Join(dir, "wiki-1.2.0-linux-amd64.glorious-plugin")

	err := run(buildArgs(manifestPath, functionsPath, filepath.Join(dir, "fehlt.md"), binaryPath, outPath, keyPath))
	if err == nil {
		t.Fatal("erwartete Fehler bei fehlendem README, bekam nil")
	}
	if !strings.Contains(err.Error(), "README") {
		t.Fatalf("Fehlermeldung erwähnt README nicht: %v", err)
	}
}

// (c) README < 300 Zeichen → Fehler.
func TestBuildReadmeTooShort(t *testing.T) {
	dir := t.TempDir()
	_, keyPath := writeKeyPEM(t, dir)

	manifestPath := writeFile(t, dir, "manifest.json", validManifest())
	functionsPath := writeFile(t, dir, "functions.json", validFunctions())
	short := "## Beschreibung\nKurz.\n## Berechtigungen\nWenig.\n"
	if n := len([]rune(short)); n >= minReadmeRunes {
		t.Fatalf("Fixture ist %d Runes, Test braucht < %d", n, minReadmeRunes)
	}
	readmePath := writeFile(t, dir, "README.md", short)
	binaryPath := writeFile(t, dir, "plugin", "MZ fake binary")
	outPath := filepath.Join(dir, "wiki-1.2.0-linux-amd64.glorious-plugin")

	err := run(buildArgs(manifestPath, functionsPath, readmePath, binaryPath, outPath, keyPath))
	if err == nil {
		t.Fatal("erwartete Fehler bei kurzem README, bekam nil")
	}
	if !strings.Contains(err.Error(), "300") {
		t.Fatalf("Fehlermeldung nennt das Zeichenlimit nicht: %v", err)
	}
}

// (d) functions.json mit mehr als 50 Objekten → Fehler.
func TestBuildFunctionsTooManyObjects(t *testing.T) {
	dir := t.TempDir()
	_, keyPath := writeKeyPEM(t, dir)

	manifestPath := writeFile(t, dir, "manifest.json", validManifest())
	readmePath := writeFile(t, dir, "README.md", validReadme())
	binaryPath := writeFile(t, dir, "plugin", "MZ fake binary")
	outPath := filepath.Join(dir, "wiki-1.2.0-linux-amd64.glorious-plugin")

	objs := make([]FunctionObject, 0, maxPluginObjects+1)
	for i := 0; i <= maxPluginObjects; i++ {
		objs = append(objs, FunctionObject{Name: fmt.Sprintf("obj%02d", i), Actions: []string{"read"}})
	}
	data, err := json.Marshal(FunctionsFile{Version: contractFunctionsVersion, Objects: objs})
	if err != nil {
		t.Fatalf("marshal functions: %v", err)
	}
	functionsPath := writeFile(t, dir, "functions.json", string(data))

	err = run(buildArgs(manifestPath, functionsPath, readmePath, binaryPath, outPath, keyPath))
	if err == nil {
		t.Fatal("erwartete Fehler bei >50 Objekten, bekam nil")
	}
	if !strings.Contains(err.Error(), "50") {
		t.Fatalf("Fehlermeldung nennt das Objektlimit nicht: %v", err)
	}
}

// (e) Eintrags-Limit: > 1000 Einträge → Fehler.
func TestBuildEntryLimitExceeded(t *testing.T) {
	dir := t.TempDir()
	_, keyPath := writeKeyPEM(t, dir)

	manifestPath := writeFile(t, dir, "manifest.json", validManifest())
	functionsPath := writeFile(t, dir, "functions.json", validFunctions())
	readmePath := writeFile(t, dir, "README.md", validReadme())
	binaryPath := writeFile(t, dir, "plugin", "MZ fake binary")
	outPath := filepath.Join(dir, "wiki-1.2.0-linux-amd64.glorious-plugin")

	// 1001 Asset-Dateien → zusammen mit den 4 Basiseinträgen > 1000.
	assetDir := filepath.Join(dir, "many")
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for i := 0; i < maxArchiveEntries+1; i++ {
		if err := os.WriteFile(filepath.Join(assetDir, fmt.Sprintf("f%04d.txt", i)), []byte("x"), 0o644); err != nil {
			t.Fatalf("write asset %d: %v", i, err)
		}
	}

	err := run(buildArgs(manifestPath, functionsPath, readmePath, binaryPath, outPath, keyPath, "--asset", assetDir))
	if err == nil {
		t.Fatal("erwartete Fehler bei >1000 Einträgen, bekam nil")
	}
	if !strings.Contains(err.Error(), "1000") {
		t.Fatalf("Fehlermeldung nennt das Eintragslimit nicht: %v", err)
	}
}

// (e) Größen-Limit: Binary > 50 MiB → Fehler.
func TestBuildSizeLimitExceeded(t *testing.T) {
	dir := t.TempDir()
	_, keyPath := writeKeyPEM(t, dir)

	manifestPath := writeFile(t, dir, "manifest.json", validManifest())
	functionsPath := writeFile(t, dir, "functions.json", validFunctions())
	readmePath := writeFile(t, dir, "README.md", validReadme())
	outPath := filepath.Join(dir, "wiki-1.2.0-linux-amd64.glorious-plugin")

	big := make([]byte, maxFileSize+1) // 50 MiB + 1
	binaryPath := filepath.Join(dir, "plugin")
	if err := os.WriteFile(binaryPath, big, 0o755); err != nil {
		t.Fatalf("write big binary: %v", err)
	}

	err := run(buildArgs(manifestPath, functionsPath, readmePath, binaryPath, outPath, keyPath))
	if err == nil {
		t.Fatal("erwartete Fehler bei >50 MiB Binary, bekam nil")
	}
	if !strings.Contains(err.Error(), "Dateilimit") {
		t.Fatalf("Fehlermeldung nennt das Größenlimit nicht: %v", err)
	}
}

// Signing-Key-Aufnahme: PEM (PKCS#8), Base64-roh und Base64-Seed.
func TestLoadSigningKey(t *testing.T) {
	dir := t.TempDir()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	// PKCS#8-PEM.
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal pkcs8: %v", err)
	}
	pemPath := filepath.Join(dir, "key.pem")
	if err := os.WriteFile(pemPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatalf("write pem: %v", err)
	}
	got, err := loadSigningKey(pemPath)
	if err != nil || !got.Equal(priv) {
		t.Fatalf("PEM-Key nicht geladen (err=%v, equal=%v)", err, got.Equal(priv))
	}

	// Base64 des rohen 64-Byte-Keys.
	rawPath := filepath.Join(dir, "key.b64")
	if err := os.WriteFile(rawPath, []byte(base64.StdEncoding.EncodeToString(priv)), 0o600); err != nil {
		t.Fatalf("write b64: %v", err)
	}
	got, err = loadSigningKey(rawPath)
	if err != nil || !got.Equal(priv) {
		t.Fatalf("Base64-Key nicht geladen (err=%v, equal=%v)", err, got.Equal(priv))
	}

	// Base64 des 32-Byte-Seeds.
	seedPath := filepath.Join(dir, "seed.b64")
	if err := os.WriteFile(seedPath, []byte(base64.StdEncoding.EncodeToString(priv.Seed())), 0o600); err != nil {
		t.Fatalf("write seed: %v", err)
	}
	got, err = loadSigningKey(seedPath)
	if err != nil || !got.Equal(priv) {
		t.Fatalf("Seed-Key nicht geladen (err=%v, equal=%v)", err, got.Equal(priv))
	}

	// Unsinn → Fehler.
	badPath := filepath.Join(dir, "bad.key")
	if err := os.WriteFile(badPath, []byte("kein key"), 0o600); err != nil {
		t.Fatalf("write bad: %v", err)
	}
	if _, err := loadSigningKey(badPath); err == nil {
		t.Fatal("erwartete Fehler bei ungültigem Key, bekam nil")
	}
}

// Manifest-Validierung: Formate und Lizenz.
func TestParseManifestValidation(t *testing.T) {
	base := validManifest()

	cases := []struct {
		name string
		json string
		want string // Teilstring der Fehlermeldung
	}{
		{"name gross", setMutiert(t, base, "name", "Wiki"), "name"},
		{"name leer", setMutiert(t, base, "name", ""), "name"},
		{"version kein semver", setMutiert(t, base, "version", "1.2"), "SemVer"},
		{"version leer", setMutiert(t, base, "version", ""), "version"},
		{"description leer", setMutiert(t, base, "description", ""), "description"},
		{"entrypoint leer", setMutiert(t, base, "entrypoint", ""), "entrypoint"},
		{"license unbekannt", setMutiert(t, base, "license", "GPL-3.0"), "Allowlist"},
		{"license leer", setMutiert(t, base, "license", ""), "Allowlist"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseManifest([]byte(tc.json))
			if err == nil {
				t.Fatalf("erwartete Fehler (%s), bekam nil", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Fehlermeldung %q enthält %q nicht", err.Error(), tc.want)
			}
		})
	}

	if _, err := parseManifest([]byte(base)); err != nil {
		t.Fatalf("gültiges Manifest muss durchgehen: %v", err)
	}
}

// setMutiert erzeugt aus der Base-Fixture ein JSON mit einem geänderten Feld.
// Wichtig: JEDE Variante startet von der unveränderten Base, damit frühere
// Mutationen (z. B. name="Wiki") spätere Fälle nicht verfälschen.
func setMutiert(t *testing.T, base, key, val string) string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(base), &m); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	m[key] = val
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(data)
}
