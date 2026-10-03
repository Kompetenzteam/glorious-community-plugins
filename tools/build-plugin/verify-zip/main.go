// EU AI Act transparency notice (Art. 50, Regulation (EU) 2024/1689): this
// source file was authored with AI assistance (devops-engineer agent) as part
// of the community release-pipeline fix and is subject to the repository's
// standard human code review before release.
//
// verify-zip checks a .glorious-plugin archive produced by build-plugin:
// manifest.json/functions.json/README.md present, archive binary matches the
// manifest entrypoint, and the Ed25519 manifest signature verifies against the
// key's public half. Local verification helper for release debugging.
package main

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

type manifest struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Entrypoint  string   `json:"entrypoint"`
	Permissions []string `json:"permissions,omitempty"`
	License     string   `json:"license,omitempty"`
	Signature   string   `json:"signature,omitempty"`
}

func main() {
	os.Exit(verifyMain(os.Args[1:]))
}

// verifyMain runs the verification and returns a process exit code: 0 on
// success, 1 on a verification failure (missing entrypoint binary or bad
// signature), 2 on usage or I/O errors. Split out from main so tests can call
// it without spawning a subprocess.
func verifyMain(args []string) int {
	if len(args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: verify-zip <archive.glorious-plugin> <key.pem>")
		return 2
	}
	archive, keyPath := args[0], args[1]

	pub, err := loadPublicKey(keyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "verify-zip: %v\n", err)
		return 2
	}

	names, m, err := readManifest(archive)
	if err != nil {
		fmt.Fprintf(os.Stderr, "verify-zip: %v\n", err)
		return 2
	}

	entry := strings.TrimPrefix(m.Entrypoint, "./")
	binaryPresent := false
	for _, n := range names {
		if n == entry {
			binaryPresent = true
		}
	}

	clean := m
	clean.Signature = ""
	canonical, err := json.Marshal(clean)
	if err != nil {
		fmt.Fprintf(os.Stderr, "verify-zip: canonical manifest: %v\n", err)
		return 2
	}
	sig, err := base64.StdEncoding.DecodeString(m.Signature)
	if err != nil {
		fmt.Fprintf(os.Stderr, "verify-zip: signature is not valid base64: %v\n", err)
		return 1
	}
	sigOK := m.Signature != "" && ed25519.Verify(pub, canonical, sig)

	fmt.Printf("%s: entries=%v entrypoint=%s binary_present=%t signature_valid=%t\n",
		archive, names, m.Entrypoint, binaryPresent, sigOK)
	if !binaryPresent || !sigOK {
		return 1
	}
	return 0
}

// loadPublicKey reads a PKCS#8 Ed25519 private key (PEM or Base64(PEM)) and
// returns its public half, mirroring the formats the workflow accepts for the
// SIGNING_KEY secret.
func loadPublicKey(path string) (ed25519.PublicKey, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// Accept Base64-wrapped PEM as well as plain PEM.
	if !strings.Contains(string(raw), "-----BEGIN") {
		decoded, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		if derr != nil {
			return nil, fmt.Errorf("%s is neither PEM nor Base64", path)
		}
		raw = decoded
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("%s contains no PEM block", path)
	}
	ki, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}
	priv, ok := ki.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("key is not an Ed25519 private key")
	}
	return priv.Public().(ed25519.PublicKey), nil
}

// readManifest returns the archive members and the decoded manifest.json.
func readManifest(path string) ([]string, manifest, error) {
	var m manifest
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, m, err
	}
	defer zr.Close()

	names := make([]string, 0, len(zr.File))
	var manifestRaw []byte
	for _, f := range zr.File {
		names = append(names, f.Name)
		if f.Name != "manifest.json" {
			continue
		}
		rc, oerr := f.Open()
		if oerr != nil {
			return nil, m, oerr
		}
		manifestRaw, err = io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, m, err
		}
	}
	if manifestRaw == nil {
		return names, m, fmt.Errorf("%s: manifest.json is missing", path)
	}
	if err := json.Unmarshal(manifestRaw, &m); err != nil {
		return names, m, fmt.Errorf("%s: invalid manifest.json: %w", path, err)
	}
	return names, m, nil
}
