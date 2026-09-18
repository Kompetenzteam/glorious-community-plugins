# Gitea-Repo & Push-Mirror nach GitHub — Einrichtung

> **Transparenzhinweis (EU-AI-Act Art. 50):** Dieses Dokument wurde mit Unterstuetzung
> eines KI-Systems erstellt und redaktionell geprueft. Es beschreibt den eingerichteten
> Zustand der Repository-Spiegelung. Enthalten sind **keine** Zugangsdaten.

## Rollenverteilung

| Rolle | Ort |
|---|---|
| Primaeres Repo (Source of Truth) | Gitea: `http://localhost:3000/Kompetenzteam/glorious-community-plugins` |
| Spiegel (Mirror) | GitHub: `https://github.com/Kompetenzteam/glorious-community-plugins` |

- Gitea-Repo ist **privat** (`private: true`), Default-Branch `main`.
- GitHub-Repo bleibt **public** (unveraendert).
- Spiegelrichtung: **Gitea → GitHub** (Push-Mirror). Es werden **keine** Aenderungen von
  GitHub nach Gitea zurueckgeholt. GitHub ist reiner Downstream-Spiegel.

## Mirror-Konfiguration

| Parameter | Wert |
|---|---|
| `remote_address` | `https://github.com/Kompetenzteam/glorious-community-plugins.git` |
| `remote_username` | `Kompetenzteam` |
| `sync_on_commit` | `true` (Push auf Gitea loest Spiegelung aus) |
| `interval` | `8h0m0s` (zusaetzlicher periodischer Sync alle 8 Stunden) |
| Richtung | Push-Mirror Gitea → GitHub |

Das **Mirror-Passwort** (GitHub-Token mit `repo`- und `workflow`-Scope) ist ausschliesslich
in Gitea gespeichert und wird dort bei Bedarf rotiert. Es steht **nicht** in diesem
Repository und **nicht** in der Remote-URL des lokalen Klons.

## Lokaler Klon: Remotes

Der lokale Klon hat zwei Remotes. **Beide URLs sind credential-frei** — die
Authentifizierung laeuft ueber den Git-Credential-Store der Maschine, nicht ueber
eingebettete Zugangsdaten in der URL.

```bash
git remote -v
# gitea   http://localhost:3000/Kompetenzteam/glorious-community-plugins.git
# origin  https://github.com/Kompetenzteam/glorious-community-plugins.git
```

Nach beiden Remotes pushen:

```bash
git push gitea main --follow-tags   # Primaer (Gitea)
git push origin main --follow-tags  # Spiegel (GitHub)
```

## Sync manuell ausloesen

Gitea 1.27.2 macht bei der Anlage eines Push-Mirrors **keinen** Initial-Sync. Manuell
ausloesen per API (Basic-Auth mit Gitea-Admin-User/Passwort — der API-Token hat hierfuer
nicht die noetigen Rechte):

```bash
# Endpunkt (Gitea 1.27.2): POST .../push_mirrors-sync
curl -u "$GITEA_USER:$GITEA_PASSWORD" -X POST \
  "http://localhost:3000/api/v1/repos/Kompetenzteam/glorious-community-plugins/push_mirrors-sync"
```

Alternativ in der Gitea-Web-UI: **Repository → Einstellungen → Repository →
Push-Mirror-Einstellungen → „Jetzt synchronisieren“**.

> Hinweis: Der in aelteren Notizen genannte Pfad
> `POST .../push_mirrors/{name}/sync` existiert in Gitea 1.27.2 **nicht** (HTTP 404).
> Gueltig sind nur `GET|POST .../push_mirrors` und `POST .../push_mirrors-sync`
> (siehe `/swagger.v1.json` der Instanz).

## Zustand pruefen

```bash
# 1) Mirror-Konfiguration + letzter Sync + Fehler
curl -u "$GITEA_USER:$GITEA_PASSWORD" \
  "http://localhost:3000/api/v1/repos/Kompetenzteam/glorious-community-plugins/push_mirrors"
# Erwartung: "last_error": "" und "last_update" != "1970-01-01T00:00:00Z"

# 2) SHA-Vergleich Gitea vs. GitHub
git ls-remote gitea  refs/heads/main
git ls-remote origin refs/heads/main
```

Beide Zeilen muessen denselben SHA liefern. Erst dann ist die Spiegelung nachweislich
aktuell.

## Bekannte Stolpersteine

- **Voraussetzung:** In der Gitea `app.ini` muss
  `GITEA__migrations__ALLOWED_DOMAINS=github.com` gesetzt sein, sonst wird das
  Mirror-Ziel abgelehnt.
- **Repo-Anlage und Mirror-Anlage** funktionieren per API nur mit **Passwort-Basic-Auth**
  (`USER:PASSWORD`). Der Gitea-API-Token hat nur `write:repository`/`read:user` und
  liefert bei User- und Mirror-Endpunkten **403**.
- **Credential-Store:** Speichert keine `localhost:3000`-Eintraege in der Standarddatei
  zuverlaessig. Fuer nicht-interaktive Pushes den Pfad ueber `GIT_ASKPASS` oder einen
  expliziten `credential.helper = store --file <datei>` verwenden.

## Sicherheitsregeln

- **Keine** Tokens/Passwoerter in Dateien, URLs oder Commits. Remote-URLs bleiben
  credential-frei (`git remote get-url gitea` muss ohne `user:pass@` ausgeben).
- GitHub-Repo **nicht** oeffentlich/privater stellen ohne Ruecksprache.
- Bestehende Branches und Tags nicht loeschen.
