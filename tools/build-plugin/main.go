// EU AI Act transparency notice (Art. 50, Regulation (EU) 2024/1689): this
// source file was authored with AI assistance (backend-developer agent) as
// part of the community reference-plugin 1.0.0 work and is subject to the
// repository's standard human code review before release.
//
// Command build-plugin is the Glorious Platform community plugin builder:
// it validates manifest.json, functions.json and README.md, signs the
// manifest with an Ed25519 key and packs everything into a
// .glorious-plugin ZIP archive that the marketplace installer accepts.
//
// The tool is a standalone module (Go stdlib only) and lives in
// marketplace/community/tools/build-plugin — it is used by the CI workflow
// template (marketplace/community/.github/workflows/release.yml) and locally
// by plugin authors.
//
// The validation and signing logic mirrors the app contracts exactly:
//
//   - Manifest: internal/plugins/manifest.go (ParseManifest, canonicalJSON,
//     SignManifest) — same struct order, same JSON tags, signature over the
//     canonical JSON without the signature field.
//   - functions.json: internal/plugins/manifest.go (ParseFunctionsFile,
//     DefaultLimits 50 objects / 10 actions, validateProfileFields with the
//     profile-field limits) + internal/rbac (system actions and roles).
//   - README: internal/pluginstore/readme.go (ValidateReadme: >= 300 runes,
//     sections "## Beschreibung" and "## Berechtigungen").
//   - Archive: Plan §3 — ZIP with manifest.json, functions.json, README.md,
//     the binary and optional assets/, <= 50 MiB uncompressed, <= 1000
//     entries, no symlinks, no path escapes.
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

	"glorious-community/tools/build-plugin/internal/pluginmanifest"
)

// Version is the tool version, printed with --version.
const version = "1.0.0"

// Archive and contract limits — mirrors of the app constants:
// internal/plugins/manifest.go (50/10), internal/pluginstore/store.go
// (50 MiB) and Plan §3 (1000 entries).
const (
	contractFunctionsVersion = "1.0.0"
	maxPluginObjects         = 50
	maxObjectActions         = 10
	minReadmeRunes           = 300
	maxArchiveSize           = 50 << 20 // 50 MiB uncompressed content
	maxFileSize              = 50 << 20 // 50 MiB per entry
	maxArchiveEntries        = 1000

	// Profile-field contract limits (functions.json "profile_fields") — exact
	// mirrors of internal/plugins/manifest.go. Text lengths are counted in
	// runes (UTF-8-safe, so umlauts count as one character).
	maxProfileFieldTextLen    = 200
	maxProfileFieldOptions    = 50
	maxProfileFieldOptionLen  = 100
	maxProfileFieldsPerPlugin = 100
)

// Known profile-field types (internal/plugins/manifest.go ProfileFieldType*).
const (
	profileFieldTypeText     = "text"
	profileFieldTypeTextarea = "textarea"
	profileFieldTypeNumber   = "number"
	profileFieldTypeSelect   = "select"
)

// profileFieldNamePattern is the allowed field-name format (max 64 chars):
// lowercase letter/digit first, then lowercase letters, digits, "_" or "-"
// — identical to internal/plugins/manifest.go.
var profileFieldNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// knownActions is the system action set (internal/rbac, AllActions).
var knownActions = []string{"read", "create", "update", "delete", "execute", "manage", "review", "grant", "write"}

// knownRoles is the system role set (internal/rbac, AllRoleNames).
var knownRoles = []string{"admin", "user", "operator", "auditor", "plugin_reviewer"}

// allowedLicenses is the marketplace license allowlist
// (internal/pluginstore/install.go), case-insensitive.
var allowedLicenses = map[string]bool{
	"mit":          true,
	"apache-2.0":   true,
	"bsd-2-clause": true,
	"bsd-3-clause": true,
	"mpl-2.0":      true,
}

// Name and version formats — identical to internal/plugins/manifest.go.
var (
	pluginNamePattern    = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)
	pluginVersionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
)

// Manifest describes a plugin — struct order and JSON tags exactly like
// internal/plugins/manifest.go, so that the canonical JSON serialization is
// byte-identical to the app and signatures are mutually verifiable. The type
// is shared with verify-zip via internal/pluginmanifest to avoid a silent
// duplicate of the manifest model across the two tools.
type Manifest = pluginmanifest.Manifest

// FunctionsFile is the RBAC permission contract (functions.json) — exactly
// like internal/plugins/manifest.go, including the optional profile_fields
// section.
type FunctionsFile struct {
	Version string           `json:"version"`
	Objects []FunctionObject `json:"objects"`
	// ProfileFields is the mirror image of the host's profile_fields contract
	// (internal/plugins/manifest.go FunctionsFile.ProfileFields) — optional.
	ProfileFields []ProfileFieldSpec `json:"profile_fields,omitempty"`
}

// ProfileFieldSpec is a single plugin profile field (functions.json
// "profile_fields") — mirror of internal/plugins/manifest.go ProfileFieldSpec:
// Name (machine key), Label (display text), Type, Options (select only),
// Group (section) and Hint (help text).
type ProfileFieldSpec struct {
	Name    string   `json:"name"`
	Label   string   `json:"label"`
	Type    string   `json:"type"`
	Options []string `json:"options,omitempty"`
	Group   string   `json:"group,omitempty"`
	Hint    string   `json:"hint,omitempty"`
}

// FunctionObject is a single permission object (bare name without the
// "plugin." prefix; the full namespace is built by the app at registration time).
type FunctionObject struct {
	Name                   string              `json:"name"`
	Description            string              `json:"description,omitempty"`
	Actions                []string            `json:"actions"`
	DefaultRolePermissions map[string][]string `json:"default_role_permissions,omitempty"`
}

// archiveEntry is a file in the target ZIP with its entry name and mode.
type archiveEntry struct {
	name string
	data []byte
	mode fs.FileMode
}

// stringSliceFlag allows repeated flags (--asset a --asset b).
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

// run encapsulates the CLI logic (testable without starting a process).
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

	// Mandatory fields of the build pipeline.
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

	// 1. Read, validate and sign the manifest.
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

	// 2. Validate functions.json against the RBAC contract (limits 50/10).
	functionsData, err := os.ReadFile(functionsPath)
	if err != nil {
		return fmt.Errorf("read functions.json: %w", err)
	}
	if _, err := parseFunctionsFile(functionsData); err != nil {
		return err
	}

	// 3. Validate the README against the minimum requirements.
	readmeData, err := os.ReadFile(readmePath)
	if err != nil {
		return fmt.Errorf("read README.md: %w", err)
	}
	if err := validateReadme(string(readmeData)); err != nil {
		return err
	}

	// 4. Check the binary (mandatory entry, started by the entrypoint).
	binaryData, err := os.ReadFile(binaryPath)
	if err != nil {
		return fmt.Errorf("read binary: %w", err)
	}
	binaryName := filepath.Base(binaryPath)
	if binaryName == "." || binaryName == "" {
		return errors.New("binary: ungültiger Dateiname")
	}

	// Fail-closed: the manifest entrypoint must match the binary name in the
	// archive, otherwise the installer cannot start it. The release workflow
	// patches manifest.build.json per platform BEFORE packing, so a mismatch
	// here is always a real authoring bug, never a legitimate platform patch.
	entryBase := filepath.Base(strings.TrimPrefix(m.Entrypoint, "./"))
	if entryBase != binaryName {
		return fmt.Errorf("entrypoint %q does not match the binary name %q in the archive (rename the binary or fix manifest.json entrypoint)", m.Entrypoint, binaryName)
	}

	// 5. Assemble the entries (order as in Plan §3).
	entries := []archiveEntry{
		{name: "manifest.json", data: mustMarshal(m), mode: 0o644},
		{name: "functions.json", data: functionsData, mode: 0o644},
		{name: "README.md", data: readmeData, mode: 0o644},
		{name: binaryName, data: binaryData, mode: 0o755},
	}

	// 6. Optional icon under assets/.
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

// parseManifest validates the manifest's mandatory fields — analogous to
// internal/plugins/manifest.go ParseManifest + validate, extended by the
// license allowlist (internal/pluginstore/install.go), because the app rejects
// empty or unknown licenses on install.
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

// canonicalManifestJSON returns the manifest as deterministic JSON with the
// signature field cleared — delegates to the shared type's CanonicalJSON so
// packer and verifier are guaranteed to build the same bytes.
func canonicalManifestJSON(m *Manifest) ([]byte, error) {
	return m.CanonicalJSON()
}

// signManifest signs the canonical manifest JSON with the Ed25519 key and
// stores the Base64 signature (StdEncoding) in the manifest — exactly like
// internal/plugins/manifest.go SignManifest.
func signManifest(m *Manifest, priv ed25519.PrivateKey) error {
	canonical, err := canonicalManifestJSON(m)
	if err != nil {
		return fmt.Errorf("sign manifest: %w", err)
	}
	m.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(priv, canonical))
	return nil
}

// parseFunctionsFile validates the RBAC contract against the default limits
// (50 objects, 10 actions) — analogous to internal/plugins/manifest.go
// ParseFunctionsFile with DefaultLimits.
func parseFunctionsFile(data []byte) (*FunctionsFile, error) {
	var ff FunctionsFile
	if err := json.Unmarshal(data, &ff); err != nil {
		return nil, fmt.Errorf("functions.json: %w", err)
	}
	if ff.Version != contractFunctionsVersion {
		return nil, fmt.Errorf("functions.json: Vertragsversion %q nicht unterstützt (gewünscht %q)", ff.Version, contractFunctionsVersion)
	}
	if len(ff.Objects) > maxPluginObjects {
		return nil, fmt.Errorf("functions.json: %d Objekte überschreiten das Maximum von %d", len(ff.Objects), maxPluginObjects)
	}
	if len(ff.Objects) == 0 && len(ff.ProfileFields) == 0 {
		return nil, errors.New("functions.json: mindestens ein Objekt oder Profilfeld erforderlich")
	}
	seen := make(map[string]bool, len(ff.Objects))
	for _, obj := range ff.Objects {
		if err := validateObject(obj, seen); err != nil {
			return nil, err
		}
	}
	if err := validateProfileFields(ff.ProfileFields); err != nil {
		return nil, err
	}
	return &ff, nil
}

// validateProfileFields checks the profile_fields section exactly against the
// host contract (internal/plugins/manifest.go validateProfileFields): unique,
// well-formed names; mandatory label with a text-length limit; known type;
// select requires options (max 50, max 100 runes each), other types must not
// carry options; bounded group/hint; field upper limit. Every message names
// the 0-based field index so authors can find the line in the JSON quickly.
func validateProfileFields(fields []ProfileFieldSpec) error {
	if len(fields) > maxProfileFieldsPerPlugin {
		return fmt.Errorf("functions.json: %d Profilfelder überschreiten das Maximum von %d", len(fields), maxProfileFieldsPerPlugin)
	}
	seen := make(map[string]bool, len(fields))
	for i, pf := range fields {
		if strings.TrimSpace(pf.Name) == "" {
			return fmt.Errorf("functions.json: Profilfeld[%d]: name ist Pflicht", i)
		}
		if !profileFieldNamePattern.MatchString(pf.Name) {
			return fmt.Errorf("functions.json: Profilfeld[%d]: name %q muss %s entsprechen", i, pf.Name, profileFieldNamePattern)
		}
		if seen[pf.Name] {
			return fmt.Errorf("functions.json: Profilfeld[%d]: doppelter Feldname %q", i, pf.Name)
		}
		seen[pf.Name] = true

		if strings.TrimSpace(pf.Label) == "" {
			return fmt.Errorf("functions.json: Profilfeld[%d] (%s): label ist Pflicht", i, pf.Name)
		}
		if utf8.RuneCountInString(pf.Label) > maxProfileFieldTextLen {
			return fmt.Errorf("functions.json: Profilfeld[%d] (%s): label überschreitet %d Zeichen", i, pf.Name, maxProfileFieldTextLen)
		}
		if utf8.RuneCountInString(pf.Group) > maxProfileFieldTextLen {
			return fmt.Errorf("functions.json: Profilfeld[%d] (%s): group überschreitet %d Zeichen", i, pf.Name, maxProfileFieldTextLen)
		}
		if utf8.RuneCountInString(pf.Hint) > maxProfileFieldTextLen {
			return fmt.Errorf("functions.json: Profilfeld[%d] (%s): hint überschreitet %d Zeichen", i, pf.Name, maxProfileFieldTextLen)
		}

		switch pf.Type {
		case profileFieldTypeText, profileFieldTypeTextarea, profileFieldTypeNumber:
			if len(pf.Options) > 0 {
				return fmt.Errorf("functions.json: Profilfeld[%d] (%s): Typ %q darf keine options deklarieren", i, pf.Name, pf.Type)
			}
		case profileFieldTypeSelect:
			if len(pf.Options) == 0 {
				return fmt.Errorf("functions.json: Profilfeld[%d] (%s): select braucht mindestens eine Option", i, pf.Name)
			}
			if len(pf.Options) > maxProfileFieldOptions {
				return fmt.Errorf("functions.json: Profilfeld[%d] (%s): %d Optionen überschreiten das Maximum von %d", i, pf.Name, len(pf.Options), maxProfileFieldOptions)
			}
			optSeen := make(map[string]bool, len(pf.Options))
			for _, opt := range pf.Options {
				if utf8.RuneCountInString(opt) > maxProfileFieldOptionLen {
					return fmt.Errorf("functions.json: Profilfeld[%d] (%s): Option überschreitet %d Zeichen", i, pf.Name, maxProfileFieldOptionLen)
				}
				if optSeen[opt] {
					return fmt.Errorf("functions.json: Profilfeld[%d] (%s): doppelte Option %q", i, pf.Name, opt)
				}
				optSeen[opt] = true
			}
		default:
			return fmt.Errorf("functions.json: Profilfeld[%d] (%s): unbekannter Typ %q (erlaubt: text, textarea, number, select)", i, pf.Name, pf.Type)
		}
	}
	return nil
}

// validateObject checks a permission object against the contract: bare name
// (no dot), system actions, no wildcards except admin, system roles.
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
				continue // the only allowed wildcard
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

// validateReadme checks the minimum requirements (internal/pluginstore/
// readme.go): >= 300 runes (UTF-8 safe) and both mandatory sections
// case-insensitively.
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

// loadSigningKey reads the Ed25519 key from a file. Accepted are PKCS#8 PEM
// ("BEGIN PRIVATE KEY") as well as Base64 (StdEncoding) of the raw 64-byte
// private key or the 32-byte seed.
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
	// No PEM: Base64 (StdEncoding) of a raw key or seed.
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

// collectAssets reads a directory recursively and returns archive entries
// under assets/ (forward slashes). Symlinks and non-regular files are
// rejected — the installer refuses symlinks anyway (install.go).
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

// buildArchive writes the entries as a ZIP (deflate) and enforces the archive
// limits: <= 1000 entries, <= 50 MiB per file, <= 50 MiB total uncompressed
// content, no path escapes.
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
			_ = f.Close() // best-effort cleanup: the primary error is returned (G104)
			return fmt.Errorf("zip: %w", err)
		}
		if _, err := w.Write(e.data); err != nil {
			_ = f.Close() // best-effort cleanup: the primary error is returned (G104)
			return fmt.Errorf("zip %q: %w", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		_ = f.Close() // best-effort cleanup: the primary error is returned (G104)
		return fmt.Errorf("zip close: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close archive: %w", err)
	}
	return nil
}

// validateEntryName rejects path escapes (mirror of the installer check
// validateEntryPath): no absolute paths, no backslashes, no ".." segments, no
// empty segments.
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

// mustMarshal serializes the (signed) manifest for the ZIP entry.
// json.Marshal on structs is deterministic; the error path is unreachable
// (only strings/slices in the struct).
func mustMarshal(m *Manifest) []byte {
	data, err := json.Marshal(m)
	if err != nil {
		panic(fmt.Sprintf("manifest marshal: %v", err))
	}
	return data
}
