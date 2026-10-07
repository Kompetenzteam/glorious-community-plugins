# routeexample — Beispiel-Plugin für den HTTP-Durchgriff

## Beschreibung

`routeexample` ist das Community-Beispiel-Plugin für den **v1.1-Contribution-
Vertrag**: Es meldet die RPC-Protokollversion **1.1** und deklariert genau eine
HTTP-Route (`GET /hello`). Damit ist es die minimale, lauffähige Referenz für
den HTTP-Durchgriff (HTTP passthrough) — den letzten Schritt der Abnahme, den
die Vorlage `hello` (RPC 1.0, keine Route) bewusst nicht abdeckt.

Das Binary ist eine **vollständige Referenz-Implementierung** (kein Dummy).

## Warum es dieses Plugin gibt

Der Host gatet die gesamte Contribution-Oberfläche fail-closed hinter der
1.1-Negotiation:

1. Der Host ruft `HandshakeService.Ping`. Meldet das Plugin `1.0`, erfolgt
   **kein** `Handshake2` — und nichts wird gemountet.
2. Bei `1.1` ruft der Host `ContributionService.Handshake2`. Nur ein Reply mit
   `PluginRPCVersion = "1.1"` passiert das Versions-Gate.
3. Danach ruft der Host `ContributionService.DescribeContributions`. Eine leere
   Routenliste mountet nichts.
4. Der Host registriert jede Route als RBAC-Regel und mountet sie unter
   `/plugins/<name>/`; der Durchgriff läuft über `WebService.ServeHTTP`.

## Startablauf (verifiziert gegen glorious-platform_v2)

1. **TLS-Material laden** — ausschließlich aus Host-Env, keine Defaults:
   - `GLORIOUS_PLUGIN_CERT_PEM` / `GLORIOUS_PLUGIN_KEY_PEM`: auf Windows
     base64-PEM; auf Unix der Pfad zu `/proc/self/fd/4` (Cert) bzw.
     `/proc/self/fd/3` (Key). Beide Transportformen werden unterstützt.
   - `GLORIOUS_PLUGIN_CA_PEM`: base64-PEM der CA (Trust anchor).
   - `GLORIOUS_PLUGIN_TLS_MIN_VERSION` (optional): `1.2` (Default) oder `1.3`.
2. **mTLS-Listener** auf `127.0.0.1:0` mit `RequireAndVerifyClientCert`.
3. **RPC-Server** mit exakt den Wire-Namen `HandshakeService`,
   `ContributionService` und `WebService`.
4. **Ready-Zeile** auf STDOUT, exakt `GLO_PLUGIN_READY 127.0.0.1:<port>`, erst
   nach erfolgreichem Start des Listeners.
5. **Handshake/Routen**: `Ping` → `1.1`; `Handshake2` → `1.1` + Identität +
   Feature `routes`; `DescribeContributions` → eine Route; `ServeHTTP`
   beantwortet den Durchgriff mit `routeexample-ok`.
6. **Fail-closed**: fehlt eine benötigte Env-Variable, wird das klar geloggt
   und der Prozess beendet sich mit Exit-Code ≠ 0.
7. **Geordnetes Herunterfahren** bei `SIGINT`/`SIGTERM`.

## Deklarierte Route

| Methode | Pfad     | Objekt         | Aktion | Body             |
| ------- | -------- | -------------- | ------ | ---------------- |
| GET     | `/hello` | `routeexample` | `read` | `routeexample-ok` |

Die Route ist **nicht** `Public` und **nicht** `Streaming`; sie ist über
`functions.json` an das RBAC-Objekt `routeexample` / Aktion `read` gebunden.

## Wire-Kontrakt

`net/rpc` kodiert Structs über den **Feldnamen** (gob). Das Community-Repo kann
`glorious-platform-v2/internal/plugins/contract` nicht importieren (internes
Paket, anderes Modul), daher spiegeln alle Wire-Structs in `main.go` die
exportierten Feldnamen des Host-Contracts zeichengenau. `main_test.go` pinnt
diese Namen per Reflection — eine Umbenennung bricht den Test, statt still auf
dem Host fehlzukodieren.

## Tests

`go build ./...`, `go vet ./...` und `go test ./...` laufen ohne Host. Die Tests
prüfen: die Route ist deklariert (nicht leer), `ServeHTTP` liefert die erwartete
Antwort bzw. 404, `Ping`/`Handshake2` melden `1.1`, die Identitätsprüfung ist
fail-closed, und die Wire-Feldnamen stimmen mit dem Host-Contract überein.

## EU AI Act, Art. 50

Die Quellen dieses Plugins wurden mit KI-Unterstützung erstellt und stehen vor
einer Freigabe unter menschlicher Prüfung.
