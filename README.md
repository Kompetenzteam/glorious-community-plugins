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
- **Binary** — `main.go` ist eine Dummy-Implementierung: loggt beim Start
  `hello-plugin: started (version 0.1.1)` und antwortet auf stdin-Eingabe `ping` mit
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

### Warum die `url` auf `127.0.0.1` zeigen muss (kein LAN-Hostname)

`platforms.<key>.url` darf in dieser privaten Dev-/LAN-Instanz **nur** ein
Loopback-Host sein (`127.0.0.1`/`localhost`), kein LAN-Name oder eine
LAN-IP wie `192.168.1.183`:

- Der Marketplace-Installer der Plattform prüft jede Index- und Asset-URL
  über `checkSecureTransport` (`internal/marketplace/sync.go`,
  `internal/marketplace/install.go`) und lehnt alles außer `https://`
  sowie `http://` auf `localhost`/`127.0.0.1` mit `ErrInsecureTransport`
  ab. Es gibt **keinen** Config-Schalter, der das öffnet.
- Der Gitea-Container veröffentlicht Port 3000 ausschließlich auf die
  Loopback-Adresse des Hosts; ein entferntes Gerät kann den Release-Link
  ohnehin nicht abrufen.
- Das Community-Repo ist **privat**: anonyme Download-Links liefern 404.
  Ein funktionierender Install braucht deshalb zusätzlich einen
  Source-Token (siehe Abschnitt 7), und der Installer sendet heute
  **keine** Credentials mit. Der Release-Link ist damit nur innerhalb
  derselben Maschine mit Token-gesichertem Git-Zugriff nützlich — siehe
  Abschnitt 7 für die vollständige Analyse und die Optionen.

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
  --manifest ../../plugins/hello/manifest.json \
  --functions ../../plugins/hello/functions.json \
  --readme ../../plugins/hello/README.md \
  --binary ../../plugins/hello/plugin.exe \
  --out hello-0.1.1-windows-amd64.glorious-plugin \
  --signing-key key.pem
```

Details zu allen Flags: `./build-plugin -h`.

## 6. Verbote

- Keine Secrets irgendeiner Art im ZIP oder im Repo (`key.pem` ist per `.gitignore`
  ausgeschlossen).
- Keine Symlinks und keine Pfad-Escapes in Archiven (der Builder lehnt sie ab).
- Keine Wildcards in `functions.json`-Aktionen außer `admin: ["*"]`.
- Keine Fat-Archive: ein ZIP pro Plattform.

## 7. Install aus einem privaten Repo — belegter Befund

Gilt für den Stand der Plattform `glorious-platform_v2` (Analyse 2026-09-26).
Alle Fundstellen relativ zum Plattform-Repo.

**Frage 1 — Felder des Source-Typs und Auth-Felder.**
`models.MarketplaceSource` (`internal/models/marketplace_source.go:23-38`) hat
`Type` (`github|gitea|generic`), `BaseURL` (`:27`), `Owner`/`Repo` (`:28-29`),
`Branch` (`:30`), `Enabled` (`:31`), `IsDefault` (`:32`) und
`TokenEncrypted` (`:33`, „fine-grained PATs for private repositories").
`TokenEncrypted` ist ein **einzelnes** Feld — es gibt **kein**
`basic_auth`, kein `header` und keine Header-Liste. `Type` ist auf
`github|gitea|generic` beschränkt (`internal/marketplace/service.go:30-32`),
d.h. ein Verzeichnis-/Datei-Source-Typ existiert nicht. Die API nimmt genau
einen Token entgegen: `sourceRequest.Token` (`internal/handler/api/marketplace_api.go:720`),
verschlüsselt ihn (`:776-783` bzw. `:821-828`) und gibt ihn **nie** zurück
(`sourceResponse`, `:674-677`).

**Frage 2 — Download-Pfad und Credentials.**
`SyncService.Sync` (`internal/marketplace/sync.go:61-114`) baut einen nackten
`GET` und setzt ausschließlich `If-None-Match` (`:78`) — kein
`Authorization`. `Downloader.Download` (`internal/marketplace/install.go:348-376`)
macht dasselbe: `http.NewRequestWithContext(..., nil)` (`:356`) und
`d.client.Do(req)` (`:360`), **ohne jeden Header**. Der Client ist ein
blanker `http.Client` ohne Transport mit Credentials —
`marketplace.NewDownloader(nil)` (`cmd/glorious-platform/main.go:946`).
`RepositoryService.SyncSource` lädt die Quelle (`service.go:197-208`) und
ruft `s.syncer.Sync(ctx, *src)` (`:225`); der Produktions-Syncer ist der
Adapter `marketplaceIndexSyncer` (`main.go:1544-1556`), der an
`SyncService.Sync` delegiert — `*src` **enthält** `TokenEncrypted`, aber
`Sync`/`Download` lesen das Feld nie und `DecryptToken`
(`service.go:388-392`, Kommentar `:386-387`: „the sync service (M2a) uses
[it] to present a decrypted token to the remote host") wird im
Produktionspfad **von keinem Aufrufer** verwendet.
`SyncService` hat kein Feld für einen TokenCipher. Kurz: **Token wird
verschlüsselt gespeichert, aber nie gesendet** — die als „fine-grained
PATs for private repositories" dokumentierte Fähigkeit ist noch nicht
verdrahtet.

**Zusatz-Blocker — Transport.** `checkSecureTransport`
(`sync.go:176-185`) lässt nur `https://` zu, plus `http://` **nur** für
`localhost`/`127.0.0.1`. Der `generic`-Typ reicht `BaseURL` als Index-URL
unverändert durch (`sync.go:161-165`), und dieselbe Prüfung läuft im
Download-Pfad (`install.go:353-355`). Das ist rein hostname-basiert: eine
URL auf einen anderen Hostnamen, der auf 127.0.0.1 auflöst, wäre erlaubt,
eine LAN-IP wie `192.168.1.183` nicht.

**Frage 3 — Ist ein Install aus einem privaten Gitea-Repo ohne weitere
Maßnahmen möglich?** **Nein.** Anonymer Abruf liefert 404 (privates Repo;
bestätigt: `curl` ohne Token → `404`, Repo-Metadaten `private: True`,
anonymer Git-Zugriff `401`). Mit Token gibt es keinen Sendepfad — und
selbst mit Token bliebe der Asset-Download unauthentifiziert, weil
`Downloader` die Source nicht kennt. Zusätzlich veröffentlicht der
Gitea-Container Port 3000 nur auf Loopback.

**Vollständigkeitskontrolle (keine stille Lücke).** Eine Suche über das
gesamte Plattform-Repo nach `Authorization`, `Bearer` und `BasicAuth`
liefert Treffer **ausschließlich** in Session-/JWT-/OIDC-/WAF-Middleware —
**kein** Treffer in `internal/marketplace/` oder in einem
HTTP-Transport, der von `Sync`/`Download` genutzt wird. Die obige Aussage
„Installer sendet keine Credentials" ist damit repo-weit belegt und nicht
nur auf die gelesenen Funktionen gestützt.

**Optionen (Trade-offs).**

1. **Community-Repo öffentlich machen.** Kleinster Eingriff, `generic`
   funktioniert sofort mit dem Release-Link; kein Plattform-Code. Preis:
   Plugin-Artefakte/Signing-Metadaten sind öffentlich, und die
   Loopback-Beschränkung bleibt — nur die eigene Maschine kann installieren
   (`192.168.1.183` wird weiter abgelehnt).
2. **Auth-Support im Source-Typ verdrahten** (kleinster echter Fix):
   `Sync`/`Download` bekommen einen optionalen Authorization-Header,
   gespeist aus `TokenEncrypted` via `DecryptToken`. Das ist genau die in
   `marketplace_source.go:18-20` versprochene Funktion; Eigentum und
   Speicherung existieren bereits. Preis: Host-Codeänderung (in diesem
   Auftrag bewusst nicht gemacht), und der 302 auf `…/releases/download/…`
   muss `localhost` in der Location behalten, damit die Transport-Prüfung
   im Redirect-Fall nicht greift.
3. **Lokaler Datei-/Verzeichnis-Source.** Umgeht HTTP, Auth und die
   Transport-Prüfung vollständig und ist für ein LAN-Studio am robustesten.
   Preis: **existiert nicht** — `Type` ist auf `github|gitea|generic`
   beschränkt (`service.go:30-32`); das ist die größte Codeänderung.
4. **LAN-Betrieb (mehrere Maschinen)** braucht zusätzlich TLS auf Gitea
   (`https://…`) oder einen Hostnamen, der auf Loopback zeigt; mit
   `http://` und einer LAN-IP ist kein Install möglich.
5. **Asset in den Index einbetten** (base64 im `index.json`, damit der
   `generic`-Typ das Plugin selbst transportiert, ohne zweite
   authentifizierte Anfrage). Beseitigt den Credential-Bedarf beim
   Download, kostet aber Indexgröße und ist mit dem 6,4-MB-Archiv und
   `maxIndexBytes` unschön.
