# index.json — Schema und Validierungsregeln

Dieses Dokument beschreibt den Repository-Vertrag `index.json` des Community-Plugin-Repositories
(`glorious-community-plugins`). Die App lädt pro Repository genau diese eine Datei (HTTPS +
ETag/304-Caching) und wählt daraus das passende Asset für `GOOS/GOARCH` aus. Ein lauffähiges
Beispiel steht in [index.example.json](./index.example.json).

Die Validierung ist **fail-closed**: Ein `index.json`, das gegen die Regeln unten verstößt, wird
von der App komplett abgelehnt (keine Teilanzeige). Fehler sind nach Priorität geordnet — der
erste Verstoß gewinnt.

---

## Repository-Ebene

| Feld | Typ | Pflicht | Regel |
|---|---|---|---|
| `format_version` | int | ja | Muss exakt `1` sein. Jede andere Version wird abgelehnt (`ErrUnsupportedFormat`). |
| `id` | string | ja | Eindeutige Repo-ID, darf nicht leer sein. Beispiel: `glorious-community`. |
| `name` | string | ja | Anzeigename des Repositories, darf nicht leer sein. |
| `maintainer` | string | nein | Betreiber/Team (freiwillig, z. B. `Glorious Platform Team`). |
| `plugins` | array | ja | Liste der Plugin-Einträge (siehe Plugin-Ebene). Leer ist erlaubt (leeres Repo). |

## Plugin-Ebene

| Feld | Typ | Pflicht | Regel |
|---|---|---|---|
| `name` | string | ja | Eindeutiger Plugin-Identifikator, darf nicht leer sein. Muss zum `manifest.json`-Namen im Asset passen (die App installiert das Asset unter diesem Namen). |
| `title` | string | ja | Anzeigename, darf nicht leer sein. |
| `description` | string | nein | Ein- bis Zwei-Satz-Beschreibung für die Katalogansicht. |
| `author` | string | nein | Autor/in oder Organisation. |
| `license` | string | nein | SPDX-Identifier (z. B. `MIT`). Die App erzwingt die Allowlist beim Installieren: `MIT`, `Apache-2.0`, `BSD-2-Clause`, `BSD-3-Clause`, `MPL-2.0` (case-insensitiv). |
| `homepage` | string | nein | Projekt-URL. |
| `min_platform_version` | string | nein | Minimale Plattform-Version (SemVer), ab der das Plugin läuft. |
| `tags` | array\<string\> | nein | Freie Schlagworte für Filter/Suche. |
| `icon_url` | string | nein | HTTPS-URL des Listen-Icons (SVG empfohlen). |
| `readme_url` | string | nein | HTTPS-URL der vollständigen README (für die Detailansicht). |
| `platforms` | object | ja | **Darf nicht leer sein.** Map von Plattform-Key → Asset. |
| `latest_version` | string | nein | Aktuellste Version (SemVer) für die Anzeige. |
| `changelog` | string | nein | URL oder Freitext-Changelog. |

### Plattform-Keys

Der Key ist exakt `"<goos>-<goarch>"` (ein Asset pro Laufzeit-Paar, keine Fat-Archive):

| Key | GOOS | GOARCH |
|---|---|---|
| `windows-amd64` | windows | amd64 |
| `linux-amd64` | linux | amd64 |
| `darwin-arm64` | darwin | arm64 |

Ein fehlender Key für das eigene Laufzeit-Paar bedeutet: „Plattform nicht unterstützt“
(`ErrPlatformUnsupported`) — es gibt keinen Fallback-Kandidaten.

## Asset-Ebene (`platforms.<key>`)

| Feld | Typ | Pflicht | Regel |
|---|---|---|---|
| `url` | string | ja | Direkter Download-Link auf die `.glorious-plugin`-Datei, darf nicht leer sein. In der Praxis: `https://github.com/<org>/<repo>/releases/download/<tag>/<datei>.glorious-plugin`. |
| `sha256` | string | ja | Exakt **64 Hex-Zeichen** (`^[0-9a-fA-F]{64}$`). Wird beim Installieren gegen die heruntergeladene Datei geprüft (Transport-Schutz). Die Platzhalter in `index.example.json` sind formatgültig, aber nicht die echten Prüfsummen — vor dem Veröffentlichen mit `sha256sum` ersetzen. |
| `signer_pubkey` | string | ja (de facto) | **Base64 (StdEncoding)** des 32-Byte-Ed25519-Public-Keys, mit dem das `manifest.json` im Asset signiert ist. Die App decodiert und verifiziert damit die Manifest-Signatur vor der Installation. Der Builder gibt den Wert mit `--print-pubkey` aus. |

## Validierungsreihenfolge (wie `internal/marketplace/index.go` `ParseIndex`)

1. JSON muss wohlgeformt sein (`ErrInvalidIndex`).
2. `format_version` muss `1` sein (`ErrUnsupportedFormat`).
3. `id` und `name` (Repo) dürfen nicht leer sein.
4. Pro Plugin: `name` und `title` dürfen nicht leer sein; `platforms` darf nicht leer sein.
5. Pro Asset: `url` und `sha256` dürfen nicht leer sein; `sha256` muss 64-Hex-Format haben
   (`ErrInvalidSHA256`).

## Tipps für Autoren

- **Ein Eintrag pro Plugin**, `latest_version` immer auf die neueste veröffentlichte Version
  setzen.
- `signer_pubkey` ist **pro Autor/Key**, nicht pro Plattform: Alle Assets eines Plugins (und
  idealerweise alle Plugins eines Autors) verwenden denselben Key, damit Nutzer eine
  durchgängige Vertrauenskette sehen.
- `icon_url`/`readme_url` über `raw.githubusercontent.com` ausliefern — kostenlos und ohne
  API-Key.
- Vor jedem Push gegen diese Regeln prüfen: `python -m json.tool index.json` (Wohlgeformtheit)
  und die 64-Hex-Regel für alle `sha256`-Felder.
