# Glorious Community Plugins

Offizielles Community-Plugin-Repository der Glorious Platform: Schema, CI-Workflow,
Builder-Tooling und das Vorlagen-Plugin [`hello`](./plugins/hello/) für
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
├── plugins/hello/                ← Vorlagen-Plugin (Entwickler-Beispiel)
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

Unter [`plugins/hello/`](./plugins/hello/) liegt das offizielle
Vorlagen-Plugin — die minimale, lauffähige Struktur, die jedes Plugin mitbringen muss:

- **`manifest.json`** — Metadaten (`name`, `version`, `description`, `entrypoint`,
  `permissions`, `license`). Die `signature` erzeugt der Builder, niemals von Hand.
  **Nur diese 7 Felder** — weitere Metadaten (Autor, Homepage, Mindestversion) gehören
  in den `index.json`, nicht ins Manifest.
- **`functions.json`** — RBAC-Permission-Contract (Version `1.0.0`), Limits: **max. 50
  Objekte**, **max. 10 Aktionen pro Objekt**. Aktionen stammen aus dem System-Aktionsset
  (`read`, `create`, `update`, `delete`, `execute`, `manage`, `review`, `grant`,
  `write`); Wildcards nur für die `admin`-Rolle. Optional ergänzt die Sektion
  `profile_fields` Plugin-registrierte Benutzerprofil-Felder (`name`, `label`, `type`,
  `options`, `group`, `hint`; max. 100 Felder pro Plugin).
- **`README.md`** — Pflicht (maschinell validiert): ≥ 300 Zeichen und die Sektionen
  `## Beschreibung` + `## Berechtigungen`.
- **Binary** — `main.go` ist eine **vollständige Referenz-Implementierung des
  Plugin-Vertrags** (Stand 1.0.0): Es lädt die vom Host provisionierte
  mTLS-Identität, startet einen TLS-Listener auf `127.0.0.1:0` mit
  `RequireAndVerifyClientCert`, registriert den `HandshakeService` (Ping +
  Handshake2-Feature-Aushandlung) über `net/rpc` und meldet seine Adresse als
  Ready-Zeile `GLO_PLUGIN_READY 127.0.0.1:<port>` auf stdout. Der Entrypoint im
  Manifest muss exakt zum Binary-Namen im ZIP passen
  (`--binary hello.exe` → `hello.exe` im ZIP, Entrypoint `./hello.exe`). Der Host
  löst den Entrypoint wörtlich auf (keine `.exe`-Inferenz); der Release-Workflow
  patcht dafür eine Manifest-Kopie je Plattform (`./hello` unter Unix,
  `./hello.exe` unter Windows). Ein Rezept für den manuellen ZIP-Bau ohne
  Release-Workflow steht in §5.

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

**Ablauf für Autoren** (das Vorlagen-Plugin `hello` unter `plugins/hello/`
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

### Signing-Key (PFLICHT-Secret)

Der Ed25519-Private-Key **muss** als Repository-Secret `SIGNING_KEY` hinterlegt sein
(Settings → Secrets and variables → Actions), sonst ist der Release-Weg gesperrt.
Er wird **niemals committet**:

```bash
openssl genpkey -algorithm ed25519 -out key.pem
gh secret set SIGNING_KEY --repo Kompetenzteam/glorious-community-plugins < key.pem
```

In Gitea (Source of Truth): *Repository → Settings → Actions → Secrets → New secret*,
Name `SIGNING_KEY`, Wert = vollständiger Inhalt von `key.pem` (PEM oder Base64(PEM)).

**Symptom bei fehlendem Secret:** der Workflow bricht im Schritt *„Signing-Key
bereitstellen"* mit `::error::Repository-Secret SIGNING_KEY fehlt oder ist leer.`
ab — **vor** jedem Matrix-Build. Früher zeigte sich dasselbe Problem erst spät im
Schritt *„Plugin-ZIP bauen, validieren und signieren"* als kryptischer Fehler
`build-plugin: signing key: Base64-Key hat 0 Bytes, gewünscht 64 (roh) oder 32 (Seed)`
und riss alle drei Matrix-Beine mit. Der neue Fail-Fast ersetzt genau diesen Fall.

Den öffentlichen Schlüssel (Base64) erhält man mit
`build-plugin --print-pubkey --signing-key key.pem` — er kommt als `signer_pubkey` in
den `index.json`. Der Builder akzeptiert PKCS#8-PEM und Base64 davon.

### Publish-Secret (für Release + index.json-Sync)

CI läuft auf **Gitea** (Source of Truth, `http://localhost:3000/...`). Die Schritte
„Release-Assets hochladen" und „index.json committen und pushen" brauchen daher ein
`GITEA_TOKEN` (Repo-Scope `write:repository`, Repo-Admin für Releases):

```bash
gh secret set GITEA_TOKEN --repo Kompetenzteam/glorious-community-plugins < token.txt   # GitHub-CLI gegen Gitea
```

bzw. Gitea-UI wie oben. Ohne dieses Secret bricht der Publish-Schritt mit
`::error::Publish-Secret GITEA_TOKEN fehlt` ab. Ein reiner `github.token` reicht
**nicht**: das Release und `index.json` liegen auf Gitea, GitHub ist nur der
Push-Mirror.

## 5. Lokal bauen (ohne CI)

### 5a. Mit dem Builder-Tool (empfohlen)

```bash
cd tools/build-plugin
go build -o build-plugin .
./build-plugin \
  --manifest ../../plugins/hello/manifest.json \
  --functions ../../plugins/hello/functions.json \
  --readme ../../plugins/hello/README.md \
  --binary ../../plugins/hello/hello.exe \
  --out hello-1.0.0-windows-amd64.glorious-plugin \
  --signing-key key.pem
```

Details zu allen Flags: `./build-plugin -h`.

### 5b. Manuelles ZIP-Rezept (ohne GitHub-Release)

Wer das `.glorious-plugin` von Hand bauen will, muss die Archivstruktur und den
Entrypoint **exakt** treffen — der Installer prüft beides fail-closed:

**Regeln**

1. Das ZIP enthält am **Root** (nicht in einem Unterordner!) genau:
   `manifest.json`, `functions.json`, `README.md` und das Binary. Der
   Manifest-`entrypoint` löst **wörtlich** relativ zum Plugin-Arbeitsverzeichnis
   auf — es gibt **keine** `.exe`-Inferenz.
2. Der **Binary-Name im ZIP-Root muss der Entrypoint sein**:
   - Windows: Binary heißt `hello.exe`, Entrypoint `"./hello.exe"`.
   - Unix (linux/darwin): Binary heißt `hello`, Entrypoint `"./hello"`.
   Ein einzelnes Manifest deckt beide nicht ab — deshalb je Plattform eine
   Manifest-Kopie mit passendem `entrypoint` verwenden (so macht es auch
   `release.yml`).
3. Das Binary muss **ausführbar** sein (`chmod +x`) bzw. im ZIP das Modus-Bit
   `0755` tragen; alle anderen Einträge `0644`.
4. Das Manifest muss **signiert** sein (`signature` = Base64-Ed25519 über das
   kanonische JSON mit geleertem `signature`-Feld). Ohne gültige Signatur
   lehnt der Installer ab. Zum Signieren entweder `build-plugin` mit
   `--signing-key` nutzen oder die Signatur wie in
   `tools/build-plugin/main.go` (`canonicalManifestJSON` + `signManifest`)
   nachbauen.
5. Grenzwerte: ≤ 50 MiB unkomprimiert, ≤ 1000 Einträge, <= 50 MiB pro Eintrag,
   **keine** Symlinks, **keine** Pfad-Escapes (`..`, absolute Pfade,
   Backslashes). Optional liegen Icon/Assets unter `assets/`.

**Beispiel (Unix, mit `zip`)**

```bash
# In einem Baustein-Verzeichnis mit manifest.json/functions.json/README.md/hello
zip -X hello-1.0.0-linux-amd64.glorious-plugin \
  manifest.json functions.json README.md hello
```

**Beispiel (Windows, mit PowerShell `Compress-Archive`)**

```powershell
Compress-Archive -Path manifest.json,functions.json,README.md,hello.exe `
  -DestinationPath hello-1.0.0-windows-amd64.glorious-plugin
```

> Achtung: `Compress-Archive` setzt keine Unix-Modus-Bits. Der Installer behandelt
> das Binary anhand des `entrypoint`-Namens und der Plattform; der offizielle Weg
> bleibt der Builder (§5a), der die Modus-Bits korrekt setzt. Für reproduzierbare,
> signierte Artefakte daher immer `build-plugin` verwenden.

Den `sha256` des fertigen ZIP (`sha256sum <datei>` bzw. `Get-FileHash`) und den
`signer_pubkey` (`build-plugin --print-pubkey`) in `index.json` eintragen (§3).

## 6. Eine neue Sprache ergänzen (i18n)

Die Referenz-Vorlage `hello` liefert ihre Meldungen über ein minimales
Nachrichten-System in [`plugins/hello/internal/i18n`](./plugins/hello/internal/i18n)
aus. Alle Meldungen laufen über **Message-Keys**, nie über literale Strings im
Code:

- `internal/i18n/messages.go` deklariert die Keys (Typ `Key`, z. B.
  `KeyIdentityMismatch`) und die Tabelle `messages` (`Locale → Key → string`).
- `i18n.Message(key)` liefert die Meldung für die aktive Locale; fehlt die
  Übersetzung, wird der Key selbst zurückgegeben (sichtbar in Logs statt leer).
- Die aktive Locale liest das Paket aus der Host-Umgebung
  (`GLORIOUS_PLUGIN_LOCALE`, Werte `de` / `en`; **Default `de`**, unbekannte
  Werte fallen auf den Default zurück).

**So ergänzt man eine Sprache (Beispiel `fr`):**

1. In `internal/i18n/messages.go` eine `Locale`-Konstante ergänzen
   (`LocaleFR Locale = "fr"`), sie in `ParseLocale` aufnehmen und in
   `Locales()` zurückgeben; dann alle Keys in der `messages`-Tabelle
   übersetzen:

   ```go
   LocaleFR: {
       KeyIdentityMismatch: "l'identité ne correspond pas au plugin attendu",
       // ... jeden weiteren Key ebenfalls
   },
   ```

2. Keinen Key vergessen: `messages_test.go` prüft über `Locales()` und
   `Keys()`, dass jede Locale jeden Key (nicht-leer) enthält — die neue Locale
   wird also automatisch mitgeprüft, sobald sie in `Locales()` steht.
3. Aufrufe im Code bleiben unverändert (`i18n.Message(KeyIdentityMismatch)`) —
   es wird **kein** Literal hartkodiert.
4. Im Plugin-`README.md` dokumentieren, welche Locales unterstützt werden.

> Hinweis: Sprache der **Meldungen** (dieser Abschnitt) ist unabhängig von der
> Sprache der **Doku**. Die Vorlage hält Meldungen in `internal/i18n` zentral;
> die Doku darf DE oder EN sein, wo sie es ist.

## 7. Verbote

- Keine Secrets irgendeiner Art im ZIP oder im Repo (`key.pem` ist per `.gitignore`
  ausgeschlossen).
- Keine Symlinks und keine Pfad-Escapes in Archiven (der Builder lehnt sie ab).
- Keine Wildcards in `functions.json`-Aktionen außer `admin: ["*"]`.
- Keine Fat-Archive: ein ZIP pro Plattform.
