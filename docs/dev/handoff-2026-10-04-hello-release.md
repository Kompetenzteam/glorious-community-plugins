# Handoff — hello-Plugin: Release-Workflow defekt (2026-10-04)

> ## ⚠️ Stand-Nachtrag (2026-10-07)
>
> **Dieser Nachtrag ist die jüngste Aussage im Dokument.** Er wurde nachträglich oben
> eingefügt und lässt die Historie darunter unverändert; wo unten „Blocker" steht, ist
> der Stand vom **04.10.2026** gemeint. Belege sind gegen `main` (`474de7fe12e9`)
> gemessen, nicht gegen den damaligen Branch-Stand.
>
> ### (a) Blocker 1 — BEHOBEN
>
> Der fehlende `working-directory` im Schritt *„ZIP validieren"* ist auf `main`
> (`474de7fe12e9d213c14eafb2affd37f21741193f`) behoben. `release.yml`, Datei-Zeile 259,
> enthält jetzt `working-directory: tools/build-plugin` (Zeile 266) und den
> Modul-lokalen Build
> `go build -o ../../verify-zip ./verify-zip` (Zeile 274). Zitat:
> ```
> 259:      - name: ZIP validieren (Einträge, Entrypoint vs. Binary, Ed25519-Signatur)
> ...
> 266:        working-directory: tools/build-plugin
> 274:          go build -o ../../verify-zip ./verify-zip
> ```
> Belegt per `git show 474de7fe12e9:.github/workflows/release.yml`.
>
> ### (b) Blocker 2 — BEHOBEN
>
> Die live `index.json` auf `main` zeigt heute **nicht mehr** auf `hello-1.0.0`
> (HTTP 404), sondern auf **`hello-1.0.1`**; der Release `hello-1.0.1` existiert und
> liefert für alle drei Plattformen Assets aus. Die drei `sha256` in `index.json` sind
> bit-genau identisch mit den **selbst gemessenen** Digests der ausgelieferten
> Gitea- **und** GitHub-Assets (`sha256sum` über beide Download-Pfade):
>
> | Plattform | sha256 (Gitea == GitHub) |
> |---|---|
> | windows-amd64 | `a164599af2cd6c79a32d0bbaf03bc10623d1c1a8b34f49c9747f5c41d423c3e1` |
> | linux-amd64 | `7cda7c5142ea02e4731ec23ba874bcbc93ce998f9fcb0aa0efd562a76e86e713` |
> | darwin-arm64 | `37db09481f0f1693bb216ce45faf74a991cd221c52d0337efb7f4f5d0b80e357` |
>
> ### (c) OFFEN — Restpunkt 2 (Manifest-Version ≠ index-Version)
>
> Der `manifest.json` **aller drei** `hello-1.0.1`-Assets trägt weiterhin
> `"version": "1.0.0"`, während die `index.json` `1.0.1` führt (Wert aus dem
> Release-TAG). Ein Konsistenz-Gate `version == index` existiert nicht — der
> Widerspruch bleibt unentdeckt. **Als offener Punkt geführt, nicht erledigt.**
>
> ### Hinweis — dritter, neuerer Fehlschlag (Run 949, 2026-10-08)
>
> Run `949` (workflow_dispatch, `main` `474de7fe12e9`) hatte eine **andere** Ursache als
> der ursprüngliche Release-Blocker: die drei `release`-Legs liefen **grün** (Asset-Upload
> ok), der Job **`update-index`** starb erst danach — Schritt *„Signer-Public-Key
> ableiten"* mit `go: command not found` (`exitcode '127'`): dem Job fehlt `go` auf dem
> `PATH`. Der Fix ist separat zu führen und hat nichts mit §3/§4 dieses Dokuments zu tun.
>
> *Nachtrag erstellt 2026-10-08 12:55 +0200.*

---

**EU AI Act Art. 50:** Dieses Dokument wurde mit KI-Unterstützung erstellt.

**Vorgänger-Session:** 2026-10-03 20:17 → 2026-10-04 09:2x (Session `20261003_201718_9b5974`).
**Wichtigster Hinweis zuerst:** In dieser Session war ab ~1,95 MB Kontext **keine
Sub-Agent-Delegation mehr möglich** (`Stack overflow (used ~1954 kB)` — reproduzierbar).
Der Fix unten ist klein; in einer **frischen Session** läuft die Delegation wieder normal.

---

## 1. Auftrag und Frage

Der Nutzer fragte: *„wurde das hello plugin nun vollständig fertiggestellt oder ist noch
etwas offen?"* — und hat anschließend freigegeben: Tag `hello-1.0.0` pushen, Release
veröffentlichen, `index.json` aktualisieren, danach E2E gegen den echten Host.

**Antwort:** Der **Code ist fertig**, die **Auslieferung ist defekt**.

---

## 2. Was fertig ist (jeweils selbst nachgemessen, nicht aus Agent-Berichten)

| Bereich | Beleg |
|---|---|
| Plugin-Code | 930 Zeilen Produktivcode + 1206 Zeilen Tests: `main.go`, `internal/{config,handshake,i18n}` |
| Build + Tests | `go build ./...` Exit 0; `go test ./...` → **4/4 Pakete ok** (0.806/0.512/0.313/0.308 s), auf dem Windows-Host ausgeführt |
| Vertrag | `tls.Listen("tcp","127.0.0.1:0")` + `RequireAndVerifyClientCert`, `RegisterName("HandshakeService", &HandshakeService{})` (Zeiger, damit `Handshake2` exponiert wird), `ContributionService`, Ready-Zeile `GLO_PLUGIN_READY 127.0.0.1:<port>` |
| `M1`-Entrypoint-Falle | **gelöst**: `release.yml` patcht `manifest.build.json` je Plattform (`./hello.exe` für Windows, `./hello` für Unix) und signiert das gepatchte Manifest |
| Repo-CI | `.github/workflows/ci.yml` vorhanden, Run 781 auf `main` = success |
| Katalogdaten | `index.json` listet `hello` mit `latest_version 1.0.0` für 3 Plattformen + `sha256` + `signer_pubkey` |

## 3. DER BLOCKER — Release-Workflow bricht ab

**Tag wurde gesetzt und gepusht (freigegeben und ausgeführt):**

```
git tag -f -a hello-1.0.0 -m "…" 74fa724b3b462eb876d0f7a0de28090589cdb444
git push origin refs/tags/hello-1.0.0      → * [new tag] hello-1.0.0
Gegenprobe: refs/tags/hello-1.0.0^{} = 74fa724b…  ✓ (zeigt auf den gemergten main)
```

**Ergebnis: Run 786 (Gitea Actions) = `failure`.**

```
job 3427 release (, amd64, linux)     failure   runner=glorious-runner-2
job 3428 release (, arm64, darwin)    failure   runner=glorious-runner
job 3429 release (.exe, amd64, windows) failure runner=glorious-runner
job 3430 update-index          skipped
job 3431 mirror-github         skipped
job 3432 verify-consistency    skipped
```

**Fehlgeschlagener Schritt** (aus Job-Log 3427, `curl -u USER:PASSWORD
…/actions/jobs/3427/logs`):

```
❌ Failure - Main ZIP validieren (Einträge, Entrypoint vs. Binary, Ed25519-Signatur)
   go: cannot find main module, but found .git/config in
       /workspace/Kompetenzteam/glorious-community-plugins
```

**Root-Cause (von mir reproduziert, nicht geraten):**

`release.yml`, Schritt *„ZIP validieren (Einträge, Entrypoint vs. Binary,
Ed25519-Signatur)"* hat **kein `working-directory`** und läuft dadurch im **Repo-Root**:

```yaml
      - name: ZIP validieren (Einträge, Entrypoint vs. Binary, Ed25519-Signatur)
        shell: bash
        run: |
          go build -o verify-zip ./tools/build-plugin/verify-zip   # Root hat keine go.mod
          ./verify-zip "${{ steps.asset.outputs.asset }}" key.pem
```

Das Verzeichnis ist ein **eigenes Go-Modul**:

```
tools/build-plugin/go.mod                       module glorious-community/tools/build-plugin
tools/build-plugin/main.go
tools/build-plugin/verify-zip/main.go           package main (Unterpaket)
```

**Gegenprobe, die funktioniert:**

```
cd tools/build-plugin && go build -o ../../verify-zip ./verify-zip
→ EXIT=0, Binary vt-verify-zip ~6.087.168 B erzeugt
```

**Verursacher:** der Schritt ist **neu** und stammt aus dem Integrations-Merge `#6`
(`74fa724b`). Der ältere Workflow-Stand am Tag `hello-0.1.2` hatte ihn nicht — deshalb
lief `hello-0.1.2` damals sauber durch.

### Der Fix (klein, exakt)

```yaml
      - name: ZIP validieren (Einträge, Entrypoint vs. Binary, Ed25519-Signatur)
        shell: bash
        working-directory: tools/build-plugin          # ← fehlt
        run: |
          go build -o ../../verify-zip ./verify-zip
          ../../verify-zip "$GITHUB_WORKSPACE/${{ steps.asset.outputs.asset }}" "$GITHUB_WORKSPACE/key.pem"
```

Hinweise:
- Die beiden **Nachbarschritte** machen es vor: `working-directory: tools/build-plugin` +
  `go build -o ../../build-plugin .` → diesem Muster folgen.
- `$GITHUB_WORKSPACE` prüfen; falls auf dem Gitea-Runner nicht gesetzt, sauber relative
  Pfade verwenden (Asset und `key.pem` liegen im Repo-Root).

## 4. Zweiter Blocker — Katalog zeigt auf ein nicht existierendes Release

```
index.json (auf main) : latest_version 1.0.0
                        …/releases/download/hello-1.0.0/hello-<plattform>.glorious-plugin
Tatsächlich           : HTTP 404 für alle drei Plattformen (Release hello-1.0.0 fehlt)
Hashes in index.json  : f0098dda… / d57e9ff9… / 970336a6…  = exakt die Digests von Tag hello-0.1.2
```

Vorhandene Releases (GitHub und Gitea): `v0.1.0` (draft), `hello-0.1.1`, `hello-0.1.2`.

→ **Sobald Run 786 grün durchläuft, löst sich das:** Der `update-index`-Job schreibt
`index.json` aus den tatsächlich veröffentlichten Bytes und pusht sie auf `main`
(Zeile ~418 `git push origin HEAD:main`).

## 5. Noch nie gelaufene Schritte — bitte mitprüfen

Weil der Job vorher starb, sind diese Schritte **nie ausgeführt** worden und damit
**unbewiesen**: `update-index`, `mirror-github`, `verify-consistency` sowie
„Prüfsummen + Public Key ermitteln" und „Release-Assets hochladen".

Prüfkriterien (dieselbe Fehlerklasse): (a) korrektes `working-directory`,
(b) Pfad-Konsistenz erzeugter ↔ verwendeter Dateien, (c) sind die genutzten
GitHub-Kontextvariablen (`github.ref_name`, `github.server_url`, `github.repository`)
auf dem **Gitea**-Runner gefüllt? Der Build-Schritt lief, also sind sie es dort —
trotzdem für jeden Verwendungsort einzeln belegen.

## 6. Umgebung und Zugänge

| Sache | Wert |
|---|---|
| Repo | `Kompetenzteam/glorious-community-plugins`, Gitea `http://localhost:3000`, Remote `origin`, default `main` |
| main | `74fa724b3b462eb876d0f7a0de28090589cdb444` |
| Credentials | `//NAS/Patze/Glorious-AI-Studio/projects/Glorious-AI-Studio/credentials/gitea-admin-credentials.txt` (TOKEN; für Kommentare/Reviews Basic-Auth USER+PASSWORD — der Token hat keinen `write:issue`-Scope) |
| GitHub-PAT | `…/credentials/github-pat.txt`, Scopes `repo, workflow` (**Scope-Hinweis:** `hdrs.get("x-oauth-scopes")` liefert `None`, weil Python-Header case-sensitiv als `X-Oauth-Scopes` herauskommen — nicht daraus schließen, der PAT sei unbrauchbar) |
| Gitea-Actions-Secrets | `GH_PAT`, `GIT_TOKEN`, `SIGNING_KEY` **alle gesetzt** (in GitHub Actions liegt nur `SIGNING_KEY` — der Workflow läuft auf **Gitea**) |
| Runner | 2× Linux (`glorious-runner`, `-2`), 2× Windows (NSSM-Dienste) — capacity je 1 |
| Go | **nur** nativ im bash/MSYS-Terminal des Windows-Hosts; `export MSYS_NO_PATHCONV=1`; native Pfade `C:/…` |
| Worktrees | **immer** auf `C:/…` anlegen, nie über UNC (UNC-Worktrees verwaisten nach einem Neustart) |
| Lokale Klone | `C:/Users/Kompetenzteam/gcp-qa6c` (Arbeitskopie dieser Session; `vt-verify-zip` ist ein untracked Proben-Artefakt und darf weg) |

## 7. Angrenzend — pv2-Stand (Produktionsreife-Auftrag)

In derselben Session außerdem erledigt und **verifiziert**:

- `#82` (pv2, 86 Dateien) gemergt → master `e621bb6dfae638bb1abe5c09f2ac82fd6f9231a9`
- GitHub-Sync **repariert**: der Push-Mirror des Community-Repos scheiterte an einem
  veralteten PAT ohne `workflow`-Scope. Neuer Mirror angelegt (`remote_mirror_ojSFWUqTXM`),
  Verifikation `GitHub main == Gitea-HEAD`, **erst danach** den defekten Eintrag gelöscht.
- 4 Community-PRs geschlossen (Branch-Spitzen **selbst** per `merge-base --is-ancestor`
  gegen `main` geprüft) → 0 offene PRs
- Installer `1.2.3` aus dem gemergten Stand gebaut und **eigenhändig nachgemessen**:
  `GloriousPlatform-Setup-1.2.3.exe` 29.811.679 B,
  `sha256 7741dd9dd0b6aa723fa0c449255ffb3f8053e778a9ab47e430a7aea5accabb8c`,
  FileVersion 1.2.3 in allen drei EXEs, Launcher-Subsystem 2 (GUI)
  → `C:/Users/Kompetenzteam/gp2-wt-inst/installer/output/`
- Testserver 4321 auf den gemergten Stand gezogen, verifiziert (Login → **303**), dann auf
  Wunsch des Nutzers **gestoppt**. Testdaten: `C:/Users/Kompetenzteam/gp2-testserver-data`
  (bleiben erhalten). Build+Ressourcen: `C:/Users/Kompetenzteam/gp2-next`

**Offene DoD-Punkte (6 Kriterien):** `#6` (Gitea+GitHub-Sync) und `#5` (Backup inkl. Cloud)
erfüllt; `#1` bereitgestellt; **offen** bleiben `#2/#3/#4` — u. a. `mapKeyResolver`-Stub in
`internal/plugins/gsc/relay_delivery.go`, hostseitig fehlender `Handshake2`-Aufruf
(pv2 definiert nur die Typen → Plugin kann `1.1` melden, aber es werden noch keine
Beiträge/Routen gemountet) und die **GSC-Plugin-Phasen G1–G9**.

## 8. Nächste Schritte in Reihenfolge

1. Branch von `main` (`74fa724b`), Fix aus §3 anwenden.
2. Zusätzlich §5 prüfen (die nie gelaufenen Schritte) — sonst scheitert der Folgelauf.
3. YAML validieren (Parser) + `go build`/`go vet` aus `tools/build-plugin`.
4. PR → CI → Review → Merge (reine CI-Config-Änderung: CI grün + 1 Approval genügen).
5. Tag neu setzen: `hello-1.0.0` auf den **neuen** main-Commit (Tag löschen und neu
   pushen — der Tag-Commit bestimmt, welche Workflow-Version läuft).
6. Run beobachten; bei Erfolg prüfen: Release vorhanden, 3 Assets, `index.json`
   automatisch aktualisiert, Download-URLs erreichbar (nicht 404).
7. E2E gegen den echten Host: Plugin über den Marketplace installieren → Review →
   Start; **Anchor prüfen** — `index.json`-`signer_pubkey` `M8NWzAdHcZ7IQZtsDBwACAf8…`
   ≠ der im Testserver-Datenverzeichnis liegende Anchor `EW0OjGz…` (Stand 22.09.). Für
   einen echten E2E muss der Host den passenden Trust-Anchor haben.

**Nicht vergessen:** Der Tag `hello-1.0.0` zeigt derzeit auf einen `main`, dessen
Release-Workflow defekt ist — bis zum Fix ist er ein **Fehlschlag-Tag**, kein Release.
