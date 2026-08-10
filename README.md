# Glorious Community Plugins

Offizielles Community-Plugin-Repository der Glorious Platform: Schema, CI-Workflow,
Builder-Tooling und das Vorlagen-Plugin [`hello`](./examples/hello-plugin/) für
Plugin-Autoren.

Ein Plugin ist **eine Datei**: ein `.glorious-plugin`-ZIP (≤ 50 MiB, ≤ 1000 Einträge),
das die App herunterlädt, prüft und installiert. Die App wählt das Asset über den
`GOOS/GOARCH`-Key aus der `platforms`-Map des [`index.json`](./index.json) — es gibt
genau **ein ZIP pro Plattform** (keine Fat-Archive). Der `index.json` ist der einzige
Vertrag zwischen diesem Repository und der App.

---

## 1. Repo-Struktur

```
glorious-community-plugins/
├── index.json                    ← der Katalog (einziger Vertrag für die App)
├── index.schema.md               ← Schema + Validierungsregeln für index.json
├── README.md                     ← dieses Dokument
├── examples/hello-plugin/        ← Vorlagen-Plugin (Entwickler-Beispiel)
│   ├── manifest.json             ← Metadaten + Signatur (vom Builder erzeugt)
│   ├── functions.json            ← RBAC-Permission-Contract (Objekte/Aktionen)
│   ├── README.md                 ← Pflicht: ≥ 300 Zeichen, ## Beschreibung + ## Berechtigungen
│   ├── go.mod / main.go          ← minimales Plugin-Binary (Dummy-Implementierung)
│   └── assets/                   ← optional: Icon, i18n, statische Ressourcen
├── plugins/<name>/               ← hier legen AUTOREN neue Plugins an (siehe §4)
├── .github/workflows/release.yml ← Tag-getriggerter Build + Sign + Release
└── tools/build-plugin/           ← Builder-Tooling (Go, stdlib only)
```

Plugin-ZIPs liegen **nicht** im Git-Repo, sondern in den GitHub-Releases (kein
Clone-Volumen, kein LFS-Kontingent).

## 2. Das Vorlagen-Plugin `hello`

Unter [`examples/hello-plugin/`](./examples/hello-plugin/) liegt das offizielle
Vorlagen-Plugin — die minimale, lauffähige Struktur, die jedes Plugin mitbringen muss:

- **`manifest.json`** — Metadaten (`name`, `version`, `description`, `entrypoint`,
  `permissions`, `license`). Die `signature` erzeugt der Builder, niemals von Hand.
  **Nur diese 7 Felder** — weitere Metadaten (Autor, Homepage, Mindestversion) gehören
  in den `index.json`, nicht ins Manifest.
- **`functions.json`** — RBAC-Permission-Contract (Version `1.0.0`), Limits: **max. 50
  Objekte**, **max. 10 Aktionen pro Objekt**. Aktionen stammen aus dem System-Aktionsset
  (`read`, `create`, `update`, `delete`, `execute`, `manage`, `review`, `grant`,
  `write`); Wildcards nur für die `admin`-Rolle.
- **`README.md`** — Pflicht (maschinell validiert): ≥ 300 Zeichen und die Sektionen
  `## Beschreibung` + `## Berechtigungen`.
- **Binary** — `main.go` ist eine Dummy-Implementierung: loggt beim Start
  `hello-plugin: started (version 0.1.0)` und antwortet auf stdin-Eingabe `ping` mit
  `pong`. Der Entrypoint im Manifest (`./plugin`) muss zum Binary-Namen im ZIP passen
  (`--binary plugin.exe` → `plugin.exe` im ZIP, Entrypoint `./plugin.exe`).

## 3. index.json pflegen

Nach jedem Release einen Eintrag in [`index.json`](./index.json) aktualisieren oder
anlegen. Schema und alle Validierungsregeln: **[index.schema.md](./index.schema.md)**.

Checkliste pro Plugin-Eintrag:

1. `name`/`title`/`description`/`author`/`license`/`homepage` setzen.
2. `platforms.<key>` für jede gebaute Plattform: `url` auf das Release-Asset, `sha256`
   von `sha256sum <datei>.glorious-plugin` (64 Hex), `signer_pubkey` aus
   `build-plugin --print-pubkey` (Base64 des 32-Byte-Ed25519-Public-Keys).
3. `latest_version` und `changelog` aktualisieren.
4. Wohlgeformtheit prüfen: `python -m json.tool index.json`.

## 4. Neues Plugin veröffentlichen

**Ablauf für Autoren** (das Vorlagen-Plugin `hello` unter `examples/hello-plugin/`
kopieren und anpassen):

1. Plugin-Quellverzeichnis anlegen: `plugins/<name>/` mit `manifest.json`,
   `functions.json`, `README.md` und `go.mod` (oder `build.sh`).
2. Lokal testen: `cd tools/build-plugin && go vet ./... && go test ./...`.
3. Release-Tag pushen — der CI-Workflow
   ([`.github/workflows/release.yml`](./.github/workflows/release.yml)) ist
   tag-getriggert. **Tag-Format:** `<plugin>-<version>` mit SemVer-Version,
   z. B. `hello-0.1.0`:

   ```bash
   git tag hello-0.1.0 && git push origin hello-0.1.0
   ```

   Der Workflow baut das Binary in einer Matrix (windows-amd64, linux-amd64,
   darwin-arm64), validiert Manifest/functions.json/README, signiert mit dem
   Ed25519-Key aus dem Repository-Secret `SIGNING_KEY` und veröffentlicht ein
   GitHub-Release mit allen `.glorious-plugin`-Assets.
4. `index.json` mit den Assets aus dem Release aktualisieren (siehe §3) und committen.

### Signing-Key

Der Ed25519-Private-Key wird als Repository-Secret `SIGNING_KEY` hinterlegt (Settings →
Secrets and variables → Actions) und **niemals committet**:

```bash
openssl genpkey -algorithm ed25519 -out key.pem
gh secret set SIGNING_KEY --repo Kompetenzteam/glorious-community-plugins < key.pem
```

Den öffentlichen Schlüssel (Base64) erhält man mit
`build-plugin --print-pubkey --signing-key key.pem` — er kommt als `signer_pubkey` in
den `index.json`. Der Builder akzeptiert PKCS#8-PEM und Base64 davon.

## 5. Lokal bauen (ohne CI)

```bash
cd tools/build-plugin
go build -o build-plugin .
./build-plugin \
  --manifest ../../examples/hello-plugin/manifest.json \
  --functions ../../examples/hello-plugin/functions.json \
  --readme ../../examples/hello-plugin/README.md \
  --binary ../../examples/hello-plugin/plugin.exe \
  --out hello-0.1.0-windows-amd64.glorious-plugin \
  --signing-key key.pem
```

Details zu allen Flags: `./build-plugin -h`.

## 6. Verbote

- Keine Secrets irgendeiner Art im ZIP oder im Repo (`key.pem` ist per `.gitignore`
  ausgeschlossen).
- Keine Symlinks und keine Pfad-Escapes in Archiven (der Builder lehnt sie ab).
- Keine Wildcards in `functions.json`-Aktionen außer `admin: ["*"]`.
- Keine Fat-Archive: ein ZIP pro Plattform.
