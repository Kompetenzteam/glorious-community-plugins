# hello — Vorlagen-Plugin

## Beschreibung

Das Plugin `hello` ist die offizielle Entwickler-Vorlage für Plugins im
Community-Repository `glorious-community-plugins`. Es zeigt die minimale,
lauffähige Struktur, die jedes Plugin mitbringen muss: ein Binary
(`main.go` + `go.mod`), ein `manifest.json` mit Metadaten, eine
`functions.json` als RBAC-Permission-Contract und diese README als
Pflicht-Dokumentation.

Das Binary ist eine **vollständige Referenz-Implementierung des
Plugin-Vertrags** (seit 0.1.2, kein Dummy mehr): Beim Start lädt es die vom
Host provisionierte mTLS-Identität, startet einen TLS-Listener auf
`127.0.0.1:0` mit `RequireAndVerifyClientCert`, registriert den
`HandshakeService` über `net/rpc` und meldet seine Adresse über die
Ready-Zeile auf stdout.

Der Startablauf (verifiziert gegen `glorious-platform_v2`):

1. **TLS-Material laden** — ausschließlich aus Host-Env, keine Defaults:
   - `GLORIOUS_PLUGIN_CERT_PEM` / `GLORIOUS_PLUGIN_KEY_PEM`:
     auf Windows als base64-kodiertes PEM; auf Unix als Pfad zu
     `/proc/self/fd/4` (Cert) bzw. `/proc/self/fd/3` (Key), die das Plugin
     einliest. Beide Transportformen werden unterstützt.
   - `GLORIOUS_PLUGIN_CA_PEM`: base64-PEM der CA, gegen die der Host-Client
     verifiziert wird (Trust anchor).
   - `GLORIOUS_PLUGIN_TLS_MIN_VERSION` (optional): `1.2` (Default) oder `1.3`.
2. **mTLS-Listener** auf `127.0.0.1:0` (`tls.Listen`) mit
   `ClientAuth = RequireAndVerifyClientCert`.
3. **RPC-Server** (`rpc.NewServer`) mit
   `RegisterName("HandshakeService", …)` und
   `RegisterName("ContributionService", …)`.
4. **Ready-Zeile** auf **STDOUT**, exakt:
   `GLO_PLUGIN_READY 127.0.0.1:<port>` — erst nach erfolgreichem Start des
   Listeners. Der Host akzeptiert nur loopback-Adressen und Ports
   1024–65535; das Plugin prüft das vor dem Drucken selbst.
5. **Handshake**: Der Host verbindet sich per mTLS und ruft
   `HandshakeService.Ping` auf ("Ping-vor-Nutzung"). Das Plugin antwortet
   mit der Vertragsversion `1.0` (akzeptiert sind `1.0` und `1.1`; `1.0` ist
   die Basis-Version, die immer akzeptiert wird — die optionale
   `Handshake2`-Negotiation und die Feature-Aushandlung auf 1.1 werden von
   der Vorlage bewusst nicht belegt).
6. **Fail-closed**: Fehlt eine der benötigten Env-Variablen, loggt das
   Plugin den Fehler auf stderr und beendet sich mit Exit-Code ≠ 0 — es gibt
   keine Defaults und es wird keine Ready-Zeile gedruckt.
7. **Geordnetes Herunterfahren** bei `SIGINT`/`SIGTERM`.

### Zweiter Service

Zusätzlich zum Handshake registriert die Vorlage den
`ContributionService` mit der Methode `DescribeContributions`. Der Service
ist Teil des Host-Contracts (`internal/plugins/contract/handshake.go`), der
Host hat in dieser Version aber noch keine Aufrufstelle dafür (der
Methodensatz ist laut Kommentar dort "deliberately empty in W1"). Er ist
dennoch vertragskonform implementiert, damit der Host ihn ohne Änderung an
dieser Vorlage erreichen kann, sobald er ihn nutzt.

## Tests

`main_test.go` prüft den Vertrag mit echten Assertions und einer im Test
erzeugten PKI (CA, Server-Leaf, Client-Zertifikat):

- `TestHello_ReadyLineFormat` — Format und Adressgrenzen der Ready-Zeile.
- `TestHello_HandshakePing` — echter mTLS-Dial plus `HandshakeService.Ping`
  (erwartet Version `1.0`) und Erreichbarkeit des `ContributionService`.
- `TestHello_FailsClosedWithoutEnv` — kein Start ohne Cert/Key/CA.
- `TestHello_HandshakeWithoutClientCert` — Ablehnung ohne Client-Zertifikat.

```sh
go build ./... && go vet ./... && gofmt -l .
go test -v ./...
```

## Berechtigungen

Die `functions.json` deklariert genau ein Objekt `hello` mit einer Aktion
`execute`. Diese Aktion steht im System-Aktionsset der Plattform
(`read`, `create`, `update`, `delete`, `execute`, `manage`, `review`,
`grant`, `write`). Standardmäßig dürfen Benutzer mit der Rolle `user`
das Objekt `hello` ausführen (`execute`); Administratoren haben über
`admin: ["*"]` vollen Zugriff.

Zusätzlich registriert das Plugin über die Sektion `profile_fields` ein
Demo-Profilfeld (`favorite_color`, Typ `text`), das nach der Installation
als editierbares Feld im Benutzerprofil erscheint — ein Beispiel dafür,
wie Plugins eigene Profilfelder beisteuern können.

Das Plugin selbst verlangt im `manifest.json` keine zusätzlichen
Laufzeit-Permissions (`"permissions": []`) — es greift weder auf das
Netzwerk noch auf das Dateisystem außerhalb seines Arbeitsverzeichnisses
zu. Neue Plugins, die solche Fähigkeiten benötigen, tragen sie hier ein
(z. B. `"permissions": ["network"]`).

## Artefakte

Die ZIP-Artefakte (`.glorious-plugin`) werden nicht im Git-Repository
abgelegt, sondern über den Builder (`tools/build-plugin`) erzeugt und als
GitHub-Release-Assets veröffentlicht. Für den Build werden keine
speziellen Rechte benötigt — der Release-Workflow baut, validiert und
signiert das Plugin automatisch. Das Archiv-Layout ist: `manifest.json`,
`functions.json`, `README.md` und das Binary (`./plugin` bzw.
`plugin.exe`) im Archiv-Root (Modus 0o755), optional unter `assets/`.
