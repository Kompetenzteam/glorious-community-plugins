// Command build-plugin ist der Community-Plugin-Builder der Glorious Platform:
// Es validiert manifest.json, functions.json und README.md, signiert das
// Manifest mit einem Ed25519-Key und verpackt alles in ein
// .glorious-plugin-ZIP-Archiv, das der Marketplace-Installer akzeptiert.
//
// Das Tool ist ein eigenständiges Modul (nur Go-Stdlib) und liegt in
// marketplace/community/tools/build-plugin — es wird vom CI-Workflow-Template
// (marketplace/community/.github/workflows/release.yml) und lokal von
// Plugin-Autoren verwendet.
//
// Die Validierungs- und Signaturlogik spiegelt die App-Verträge exakt wider:
//
//   - Manifest: internal/plugins/manifest.go (ParseManifest, canonicalJSON,
//     SignManifest) — dieselbe Struct-Reihenfolge, dieselben JSON-Tags,
//     Signatur über das kanonische JSON ohne signature-Feld.
//   - functions.json: internal/plugins/manifest.go (ParseFunctionsFile,
//     DefaultLimits 50 Objekte / 10 Aktionen) + internal/rbac (System-Aktionen
//     und -Rollen).
//   - README: internal/pluginstore/readme.go (ValidateReadme: >= 300 Runes,
//     Sektionen "## Beschreibung" und "## Berechtigungen").
//   - Archiv: Plan §3 — ZIP mit manifest.json, functions.json, README.md,
//     dem Binary und optional assets/, <= 50 MiB unkomprimiert, <= 1000
//     Einträge, keine Symlinks, keine Pfad-Escapes.
package main

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

// Version ist die Tool-Version, ausgegeben mit --version.
const version = "1.0.0"

// Archiv- und Contract-Grenzwerte — Spiegel der App-Konstanten:
// internal/plugins/manifest.go (50/10), internal/pluginstore/store.go
// (50 MiB) und Plan §3 (1000 Einträge).
const (
	contractFunctionsVersion = "1.0.0"
	maxPluginObjects         = 50
	maxObjectActions         = 10
	minReadmeRunes           = 300
	maxArchiveSize           = 50 << 20 // 50 MiB unkomprimierter Inhalt
	maxFileSize              = 50 << 20 // 50 MiB pro Eintrag
	maxArchiveEntries        = 1000
)

// knownActions ist das System-Aktionsset (internal/rbac, AllActions).
var knownActions = []string{"read", "create", "update", "delete", "execute", "manage", "review", "grant", "write"}

// knownRoles ist das System-Rollenset (internal/rbac, AllRoleNames).
var knownRoles = []string{"admin", "user", "operator", "auditor", "plugin_reviewer"}

// allowedLicenses ist die Marketplace-Lizenz-Allowlist
// (internal/pluginstore/install.go), case-insensitiv.
var allowedLicenses = map[string]bool{
	"mit":          true,
	"apache-2.0":   true,
	"bsd-2-clause": true,
	"bsd-3-clause": true,
	"mpl-2.0":      true,
}

// Namens- und Versionsformate — identisch zu internal/plugins/manifest.go.
var (
	pluginNamePattern    = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	pluginVersionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
)

// Manifest beschreibt ein Plugin — Struct-Reihenfolge und JSON-Tags exakt wie
// internal/plugins/manifest.go, damit die kanonische JSON-Serialisierung byte-
// identisch zur App ist und Signaturen wechselseitig verifizierbar sind.
type Manifest struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Entrypoint  string   `json:"entrypoint"`
	Permissions []string `json:"permissions,omitempty"`
	License     string   `json:"license,omitempty"`
	Signature   string   `json:"signature,omitempty"`
}

// FunctionsFile ist der RBAC-Permission-Contract (functions.json) — exakt wie
// internal/plugins/manifest.go.
type FunctionsFile struct {
	Version string           `json:"version"`
	Objects []FunctionObject `json:"objects"`
}

// FunctionObject ist ein einzelnes Permission-Objekt (Bare-Name ohne "plugin."
// Präfix; die volle Namespace wird von der App zur Registrierungszeit gebaut).
type FunctionObject struct {
	Name                   string              `json:"name"`
	Description            string              `json:"description,omitempty"`
	Actions                []string            `json:"actions"`
	DefaultRolePermissions map[string][]string `json:"default_role_permissions,omitempty"`
}

// archiveEntry ist eine Datei im Ziel-ZIP mit ihrem Eintragsnamen und Modus.
type archiveEntry struct {
	name string
	data []byte
	mode fs.FileMode
}

// stringSliceFlag erlaubt wiederholte Flags (--asset a --asset b).
type stringSliceFlag []string

func (s *stringSliceFlag) String() string { return strings.Join(*s, ",") }
func (s *stringSliceFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "build-plugin: %v\n", err)
		os.Exit(1)
	}
}

// run kapselt die CLI-Logik (testbar ohne Prozessstart).
func run(args []string) error {
	fs := flag.NewFlagSet("build-plugin", flag.ContinueOnError)
	var (
		manifestPath   string
		functionsPath  string
		readmePath     string
		binaryPath     string
		outPath        string
		signingKeyPath string
		iconPath       string
		assetDirs      stringSliceFlag
		printPubkey    bool
		showVersion    bool
	)
	fs.StringVar(&manifestPath, "manifest", "", "Pfad zu manifest.json (Pflicht)")
	fs.StringVar(&functionsPath, "functions", "", "Pfad zu functions.json (Pflicht)")
	fs.StringVar(&readmePath, "readme", "", "Pfad zu README.md (Pflicht)")
	fs.StringVar(&binaryPath, "binary", "", "Pfad zum Plugin-Binary (Pflicht)")
	fs.StringVar(&outPath, "out", "", "Zielpfad der .glorious-plugin-Datei (Pflicht)")
	fs.StringVar(&signingKeyPath, "signing-key", "", "Ed25519-Key: PKCS#8-PEM-Datei oder Base64 (Pflicht)")
	fs.StringVar(&iconPath, "icon", "", "Optional: Icon-Datei, wird als assets/<name> gebündelt")
	fs.Var(&assetDirs, "asset", "Optional: Verzeichnis, wird rekursiv unter assets/ gebündelt (wiederholbar)")
	fs.BoolVar(&printPubkey, "print-pubkey", false, "Nur den Base64-Public-Key ausgeben (für index.json signer_pubkey) und beenden")
	fs.BoolVar(&showVersion, "version", false, "Tool-Version ausgeben und beenden")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: build-plugin --manifest manifest.json --functions functions.json\n")
		fmt.Fprintf(fs.Output(), "  --readme README.md --binary plugin.exe --out <name>-<version>-<goos>-<goarch>.glorious-plugin\n")
		fmt.Fprintf(fs.Output(), "  --signing-key key.pem [--icon icon.svg] [--asset assets/]\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if showVersion {
		fmt.Printf("build-plugin %s\n", version)
		return nil
	}
	if signingKeyPath == "" {
		return errors.New("--signing-key ist Pflicht (Ed25519-PEM-Datei oder Base64)")
	}
	priv, err := loadSigningKey(signingKeyPath)
	if err != nil {
		return fmt.Errorf("signing key: %w", err)
	}
	if printPubkey {
		pub, ok := priv.Public().(ed25519.PublicKey)
		if !ok {
			return errors.New("signing key: kein Ed25519-Public-Key ableitbar")
		}
		fmt.Println(base64.StdEncoding.EncodeToString(pub))
		return nil
	}

	// Pflichtfelder der Build-Pipeline.
	for flagName, p := range map[string]string{
		"--manifest":  manifestPath,
		"--functions": functionsPath,
		"--readme":    readmePath,
		"--binary":    binaryPath,
		"--out":       outPath,
	} {
		if p == "" {
			return fmt.Errorf("%s ist Pflicht", flagName)
		}
	}

	// 1. Manifest lesen, validieren und signieren.
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	m, err := parseManifest(manifestData)
	if err != nil {
		return err
	}
	if err := signManifest(m, priv); err != nil {
		return err
	}

	// 2. functions.json gegen den RBAC-Contract validieren (Limits 50/10).
	functionsData, err := os.ReadFile(functionsPath)
	if err != nil {
		return fmt.Errorf("read functions.json: %w", err)
	}
	if _, err := parseFunctionsFile(functionsData); err != nil {
		return err
	}

	// 3. README gegen die Mindestanforderungen validieren.
	readmeData, err := os.ReadFile(readmePath)
	if err != nil {
		return fmt.Errorf("read README.md: %w", err)
	}
	if err := validateReadme(string(readmeData)); err != nil {
		return err
	}

	// 4. Binary prüfen (Pflicht-Eintrag, der Entrypoint startet es).
	binaryData, err := os.ReadFile(binaryPath)
	if err != nil {
		return fmt.Errorf("read binary: %w", err)
	}
	binaryName := filepath.Base(binaryPath)
	if binaryName == "." || binaryName == "" {
		return errors.New("binary: ungültiger Dateiname")
	}

	// Warnung, wenn der Manifest-Entrypoint nicht zum Binary-Namen passt.
	entryBase := filepath.Base(strings.TrimPrefix(m.Entrypoint, "./"))
	if entryBase != binaryName {
		fmt.Fprintf(os.Stderr, "build-plugin: Warnung: Entrypoint %q passt nicht zum Binary-Namen %q im Archiv\n", m.Entrypoint, binaryName)
	}

	// 5. Einträge zusammenstellen (Reihenfolge wie Plan §3).
	entries := []archiveEntry{
		{name: "manifest.json", data: mustMarshal(m), mode: 0o644},
		{name: "functions.json", data: functionsData, mode: 0o644},
		{name: "README.md", data: readmeData, mode: 0o644},
		{name: binaryName, data: binaryData, mode: 0o755},
	}

	// 6. Optionales Icon unter assets/.
	if iconPath != "" {
		iconData, err := os.ReadFile(iconPath)
		if err != nil {
			return fmt.Errorf("read icon: %w", err)
		}
		entries = append(entries, archiveEntry{name: "assets/" + filepath.Base(iconPath), data: iconData, mode: 0o644})
	}

	// 7. Optionale Asset-Verzeichnisse rekursiv unter assets/.
	for _, dir := range assetDirs {
		assetEntries, err := collectAssets(dir)
		if err != nil {
			return err
		}
		entries = append(entries, assetEntries...)
	}

	// 8. Archiv schreiben (Grenzwerte in buildArchive erzwungen).
	if err := buildArchive(outPath, entries); err != nil {
		return err
	}

	total := uint64(0)
	for _, e := range entries {
		total += uint64(len(e.data))
	}
	pub, _ := priv.Public().(ed25519.PublicKey)
	fmt.Printf("build-plugin: %s-%s: %d Einträge, %.1f MiB -> %s\n",
		m.Name, m.Version, len(entries), float64(total)/(1<<20), outPath)
	fmt.Printf("signer_pubkey (für index.json signer_pubkey): %s\n", base64.StdEncoding.EncodeToString(pub))
	return nil
}

// parseManifest validiert die Pflichtfelder des Manifests — analog
// internal/plugins/manifest.go ParseManifest + validate, ergänzt um die
// Lizenz-Allowlist (internal/pluginstore/install.go), da die App leere oder
// unbekannte Lizenzen beim Installieren ablehnt.
func parseManifest(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("manifest.json: %w", err)
	}
	if strings.TrimSpace(m.Name) == "" {
		return nil, errors.New("manifest.json: name ist Pflicht")
	}
	if !pluginNamePattern.MatchString(m.Name) {
		return nil, fmt.Errorf("manifest.json: name %q muss %s entsprechen", m.Name, pluginNamePattern)
	}
	if strings.TrimSpace(m.Version) == "" {
		return nil, errors.New("manifest.json: version ist Pflicht")
	}
	if !pluginVersionPattern.MatchString(m.Version) {
		return nil, fmt.Errorf("manifest.json: version %q ist kein SemVer x.y.z", m.Version)
	}
	if strings.TrimSpace(m.Description) == "" {
		return nil, errors.New("manifest.json: description ist Pflicht")
	}
	if strings.TrimSpace(m.Entrypoint) == "" {
		return nil, errors.New("manifest.json: entrypoint ist Pflicht")
	}
	if !allowedLicenses[strings.ToLower(strings.TrimSpace(m.License))] {
		return nil, fmt.Errorf("manifest.json: license %q nicht in Allowlist (MIT, Apache-2.0, BSD-2-Clause, BSD-3-Clause, MPL-2.0)", m.License)
	}
	return &m, nil
}

// canonicalManifestJSON liefert das Manifest als deterministisches JSON mit
// geleertem signature-Feld — das identische Muster wie internal/plugins/
// manifest.go canonicalJSON: json.Marshal auf eine Struct-Kopie (feste
// Feldreihenfolge); Maps/Raw-JSON dürfen in signierten Feldern nicht
// vorkommen.
func canonicalManifestJSON(m *Manifest) ([]byte, error) {
	cleaned := *m
	cleaned.Signature = ""
	return json.Marshal(cleaned)
}

// signManifest signiert das kanonische Manifest-JSON mit dem Ed25519-Key und
// hinterlegt die Base64-Signatur (StdEncoding) im Manifest — exakt wie
// internal/plugins/manifest.go SignManifest.
func signManifest(m *Manifest, priv ed25519.PrivateKey) error {
	canonical, err := canonicalManifestJSON(m)
	if err != nil {
		return fmt.Errorf("sign manifest: %w", err)
	}
	m.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, canonical))
	return nil
}

// parseFunctionsFile validiert den RBAC-Contract gegen die Default-Limits
// (50 Objekte, 10 Aktionen) — analog internal/plugins/manifest.go
// ParseFunctionsFile mit DefaultLimits.
func parseFunctionsFile(data []byte) (*FunctionsFile, error) {
	var ff FunctionsFile
	if err := json.Unmarshal(data, &ff); err != nil {
		return nil, fmt.Errorf("functions.json: %w", err)
	}
	if ff.Version != contractFunctionsVersion {
		return nil, fmt.Errorf("functions.json: Vertragsversion %q nicht unterstützt (gewünscht %q)", ff.Version, contractFunctionsVersion)
	}
	if len(ff.Objects) == 0 {
		return nil, errors.New("functions.json: mindestens ein Objekt erforderlich")
	}
	if len(ff.Objects) > maxPluginObjects {
		return nil, fmt.Errorf("functions.json: %d Objekte überschreiten das Maximum von %d", len(ff.Objects), maxPluginObjects)
	}
	seen := make(map[string]bool, len(ff.Objects))
	for _, obj := range ff.Objects {
		if err := validateObject(obj, seen); err != nil {
			return nil, err
		}
	}
	return &ff, nil
}

// validateObject prüft ein Permission-Objekt gegen den Contract: Bare-Name
// (kein Punkt), System-Aktionen, keine Wildcards außer admin, System-Rollen.
func validateObject(o FunctionObject, seen map[string]bool) error {
	if strings.TrimSpace(o.Name) == "" {
		return errors.New("functions.json: Objektname ist Pflicht")
	}
	if strings.Contains(o.Name, ".") {
		return fmt.Errorf("functions.json: Objekt %q darf keinen Punkt enthalten (Bare-Name)", o.Name)
	}
	if !pluginNamePattern.MatchString(o.Name) {
		return fmt.Errorf("functions.json: Objektname %q muss %s entsprechen", o.Name, pluginNamePattern)
	}
	if seen[o.Name] {
		return fmt.Errorf("functions.json: doppeltes Objekt %q", o.Name)
	}
	seen[o.Name] = true

	if len(o.Actions) == 0 {
		return fmt.Errorf("functions.json: Objekt %q braucht mindestens eine Aktion", o.Name)
	}
	if len(o.Actions) > maxObjectActions {
		return fmt.Errorf("functions.json: Objekt %q hat %d Aktionen, Maximum %d", o.Name, len(o.Actions), maxObjectActions)
	}
	actionSeen := make(map[string]bool, len(o.Actions))
	for _, act := range o.Actions {
		if strings.Contains(act, "*") {
			return fmt.Errorf("functions.json: Wildcard-Aktion %q an Objekt %q nicht erlaubt", act, o.Name)
		}
		if !slices.Contains(knownActions, act) {
			return fmt.Errorf("functions.json: Aktion %q an Objekt %q nicht im System-Aktionsset", act, o.Name)
		}
		if actionSeen[act] {
			return fmt.Errorf("functions.json: doppelte Aktion %q an Objekt %q", act, o.Name)
		}
		actionSeen[act] = true
	}

	for role, actions := range o.DefaultRolePermissions {
		if !slices.Contains(knownRoles, role) {
			return fmt.Errorf("functions.json: Default-Rolle %q ist keine System-Rolle", role)
		}
		for _, act := range actions {
			if act == "*" && role == "admin" {
				continue // die einzige erlaubte Wildcard
			}
			if strings.Contains(act, "*") {
				return fmt.Errorf("functions.json: Wildcard %q für Rolle %q an Objekt %q nicht erlaubt", act, role, o.Name)
			}
			if role != "admin" && act == "manage" {
				return fmt.Errorf("functions.json: Rolle %q darf an Objekt %q kein manage als Default erhalten", role, o.Name)
			}
			if !slices.Contains(o.Actions, act) {
				return fmt.Errorf("functions.json: Rolle %q Aktion %q ist an Objekt %q nicht deklariert", role, act, o.Name)
			}
		}
	}
	return nil
}

// validateReadme prüft die Mindestanforderungen (internal/pluginstore/
// readme.go): >= 300 Runes (UTF-8-sicher) und beide Pflichtsektionen
// case-insensitiv.
func validateReadme(content string) error {
	if runes := utf8.RuneCountInString(content); runes < minReadmeRunes {
		return fmt.Errorf("README.md hat %d Zeichen, Minimum %d", runes, minReadmeRunes)
	}
	lower := strings.ToLower(content)
	for _, section := range []string{"## beschreibung", "## berechtigungen"} {
		if !strings.Contains(lower, section) {
			return fmt.Errorf("README.md fehlt die Pflichtsektion %q", section)
		}
	}
	return nil
}

// loadSigningKey liest den Ed25519-Key aus einer Datei. Akzeptiert werden
// PKCS#8-PEM ("BEGIN PRIVATE KEY") sowie Base64 (StdEncoding) des rohen
// 64-Byte-Private-Keys oder des 32-Byte-Seeds.
func loadSigningKey(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if block, _ := pem.Decode(data); block != nil {
		if block.Type != "PRIVATE KEY" {
			return nil, fmt.Errorf("PEM-Blocktyp %q nicht unterstützt (gewünscht PRIVATE KEY / PKCS#8)", block.Type)
		}
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("PKCS#8-Key nicht lesbar: %w", err)
		}
		priv, ok := key.(ed25519.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("Key ist %T, gewünscht ed25519.PrivateKey", key)
		}
		return priv, nil
	}
	// Kein PEM: Base64 (StdEncoding) eines rohen Keys oder Seeds.
	raw := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
			return -1
		}
		return r
	}, string(data))
	dec, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("weder PEM noch Base64: %w", err)
	}
	switch len(dec) {
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(dec), nil
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(dec), nil
	default:
		return nil, fmt.Errorf("Base64-Key hat %d Bytes, gewünscht %d (roh) oder %d (Seed)",
			len(dec), ed25519.PrivateKeySize, ed25519.SeedSize)
	}
}

// collectAssets liest ein Verzeichnis rekursiv und liefert Archiveinträge
// unter assets/ (Forward-Slashes). Symlinks und Nicht-Regulärdateien werden
// abgelehnt — der Installer verweigert Symlinks ohnehin (install.go).
func collectAssets(dir string) ([]archiveEntry, error) {
	var entries []archiveEntry
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(rel)
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("asset %q ist ein Symlink — nicht erlaubt", name)
		}
		if d.IsDir() {
			return validateEntryName("assets/" + name)
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("asset %q ist keine reguläre Datei", name)
		}
		if err := validateEntryName("assets/" + name); err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("asset %q: %w", name, err)
		}
		entries = append(entries, archiveEntry{name: "assets/" + name, data: data, mode: 0o644})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("asset dir %q: %w", dir, err)
	}
	return entries, nil
}

// buildArchive schreibt die Einträge als ZIP (Deflate) und erzwingt die
// Archiv-Grenzwerte: <= 1000 Einträge, <= 50 MiB pro Datei, <= 50 MiB
// unkomprimierter Gesamtinhalt, keine Pfad-Escapes.
func buildArchive(outPath string, entries []archiveEntry) error {
	if len(entries) == 0 {
		return errors.New("kein Archiveintrag")
	}
	if len(entries) > maxArchiveEntries {
		return fmt.Errorf("%d Einträge überschreiten das Maximum von %d", len(entries), maxArchiveEntries)
	}
	seen := make(map[string]bool, len(entries))
	var total uint64
	for _, e := range entries {
		if err := validateEntryName(e.name); err != nil {
			return err
		}
		if seen[e.name] {
			return fmt.Errorf("doppelter Archiveintrag %q", e.name)
		}
		seen[e.name] = true
		if uint64(len(e.data)) > maxFileSize {
			return fmt.Errorf("Eintrag %q überschreitet das Dateilimit von %d Bytes", e.name, maxFileSize)
		}
		total += uint64(len(e.data))
		if total > maxArchiveSize {
			return fmt.Errorf("unkomprimierter Inhalt überschreitet das Limit von %d Bytes (50 MiB)", maxArchiveSize)
		}
	}

	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	zw := zip.NewWriter(f)
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		hdr.SetMode(e.mode)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			_ = f.Close() // Best-Effort-Cleanup: der primäre Fehler wird zurückgegeben (G104)
			return fmt.Errorf("zip: %w", err)
		}
		if _, err := w.Write(e.data); err != nil {
			_ = f.Close() // Best-Effort-Cleanup: der primäre Fehler wird zurückgegeben (G104)
			return fmt.Errorf("zip %q: %w", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		_ = f.Close() // Best-Effort-Cleanup: der primäre Fehler wird zurückgegeben (G104)
		return fmt.Errorf("zip close: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close archive: %w", err)
	}
	return nil
}

// validateEntryName weist Pfad-Escapes zurück (Spiegel der Installer-Prüfung
// validateEntryPath): keine absoluten Pfade, keine Backslashes, keine ".."-
// Segmente, keine Leersegmente.
func validateEntryName(name string) error {
	if name == "" {
		return errors.New("leerer Archiveintragsname")
	}
	if strings.HasPrefix(name, "/") || filepath.IsAbs(name) {
		return fmt.Errorf("Eintrag %q: absolute Pfade nicht erlaubt", name)
	}
	if strings.Contains(name, "\\") {
		return fmt.Errorf("Eintrag %q: Backslash nicht erlaubt", name)
	}
	for _, seg := range strings.Split(name, "/") {
		if seg == ".." || seg == "." || seg == "" {
			return fmt.Errorf("Eintrag %q: ungültiges Pfadsegment", name)
		}
	}
	return nil
}

// mustMarshal serialisiert das (signierte) Manifest für den ZIP-Eintrag.
// json.Marshal auf Structs ist deterministisch; der Fehlerpfad ist
// unerreichbar (nur Strings/Slices im Struct).
func mustMarshal(m *Manifest) []byte {
	data, err := json.Marshal(m)
	if err != nil {
		panic(fmt.Sprintf("manifest marshal: %v", err))
	}
	return data
}
