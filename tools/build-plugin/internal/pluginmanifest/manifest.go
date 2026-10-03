// EU AI Act transparency notice (Art. 50, Regulation (EU) 2024/1689): this
// source file was authored with AI assistance (devops-engineer agent) as part
// of the community release-pipeline fix and is subject to the repository's
// standard human code review before release.
//
// Package pluginmanifest holds the single shared definition of the plugin
// manifest model used by both tools/build-plugin (the packer/signer) and
// tools/build-plugin/verify-zip (the release-time verifier).
//
// Why a shared package instead of two local types: both tools are separate
// main packages, so neither can import the other. Before this package existed
// each defined its own manifest struct with identical fields and JSON tags —
// a silent duplicate that could drift apart and break signature verification
// (the verifier rebuilds the canonical JSON that the packer signed). Keeping
// one exported type here removes that drift risk.
//
// The field order and JSON tags MUST stay byte-identical to the app contract
// internal/plugins/manifest.go, because the canonical JSON is what gets
// signed and verified.
package pluginmanifest

import "encoding/json"

// Manifest describes a plugin. Its struct order and JSON tags must remain
// byte-identical to internal/plugins/manifest.go so that the canonical JSON
// serialization matches the app and signatures are mutually verifiable.
type Manifest struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Entrypoint  string   `json:"entrypoint"`
	Permissions []string `json:"permissions,omitempty"`
	License     string   `json:"license,omitempty"`
	Signature   string   `json:"signature,omitempty"`
}

// CanonicalJSON returns the manifest as deterministic JSON with the signature
// field cleared — the identical pattern as internal/plugins/manifest.go
// canonicalJSON: json.Marshal on a struct copy (fixed field order). Maps/raw
// JSON must not occur in signed fields.
func (m *Manifest) CanonicalJSON() ([]byte, error) {
	cleaned := *m
	cleaned.Signature = ""
	return json.Marshal(cleaned)
}
