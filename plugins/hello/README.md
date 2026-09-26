# hello \u2014 Referenz-Plugin

## Beschreibung

Das Plugin `hello` ist die offizielle Entwickler-Vorlage f\u00fcr Plugins im
Community-Repository `glorious-community-plugins`. Es zeigt die minimale,
**lauff\u00e4hige** Struktur, die jedes Plugin mitbringen muss: ein Binary
(`main.go` + `go.mod`), ein `manifest.json` mit Metadaten, eine
`functions.json` als RBAC-Permission-Contract und diese README als
Pflicht-Dokumentation.

Ab Version 0.1.2 ist das Binary ein echtes Out-of-Process-Plugin und keine
Attrappe mehr. Es implementiert den Host-Vertrag aus
`internal/plugins/runtime.go` der Plattform:

1. **Identit\u00e4t aus der Umgebung.** Der Host startet das Plugin als eigenen
   OS-Prozess und \u00fcbergibt die mTLS-Identit\u00e4t \u00fcber reservierte Variablen:
   `GLORIOUS_PLUGIN_NAME`, `GLORIOUS_PLUGIN_CERT_PEM`,
   `GLORIOUS_PLUGIN_KEY_PEM`, `GLORIOUS_PLUGIN_CA_PEM` und
   `GLORIOUS_PLUGIN_TLS_MIN_VERSION`. Auf Windows (und \u00fcberall ohne
   Deskriptor-Vererbung) tragen die Variablen base64-kodiertes PEM; auf Unix
   tragen sie den Pfad `/proc/self/fd/<n>` zu einem vererbten Deskriptor. Das
   Plugin unterst\u00fctzt beide Formen.
2. **mTLS-Server.** Das Plugin lauscht auf `127.0.0.1:0`, l\u00e4sst sich den Port
   vom Kernel geben und verlangt ein Client-Zertifikat, das von der
   \u00fcbergebenen Plugin-CA signiert ist. Die TLS-Mindestversion folgt
   `GLORIOUS_PLUGIN_TLS_MIN_VERSION` (Vorgabe 1.3).
3. **Readiness-Zeile.** Nach dem Binden schreibt das Plugin
   `GLO_PLUGIN_READY 127.0.0.1:<port>` auf stdout. Der Host wartet h\u00f6chstens
   10 Sekunden darauf; ohne diese Zeile startet das Plugin nie.
4. **net/rpc-Dienste.** Das Plugin registriert `Handshake.Ping` (der Host ruft
   es direkt nach dem Verbindungsaufbau auf und pr\u00fcft die gemeldete
   Protokollversion `1.0`) sowie den Demo-Dienst `Hello.Greet`, der die in
   `functions.json` deklarierte Aktion `execute` abbildet.

`Hello.Greet` ist bewusst seiteneffektfrei: Es rendert nur einen Gru\u00df und gibt
Plugin-Name und -Version zur\u00fcck, damit eine erteilte Berechtigung
`plugin.hello.hello:execute` keinen Host-Zustand ver\u00e4ndern kann.

### Entrypoint und Dateiname

`manifest.json` nennt den Entrypoint `./plugin.exe`, weil der Windows-Build des
Release-Workflows genau diese Datei in das Archiv legt. Der Host erg\u00e4nzt keine
Dateiendungen: Er vergleicht den Manifest-Entrypoint unver\u00e4ndert mit den
Dateien im entpackten Plugin-Verzeichnis. Beim Bauen f\u00fcr Windows muss das
Binary daher `plugin.exe` hei\u00dfen, auf Linux/macOS `plugin`.

### Selbst bauen und signieren

```bash
# 1. Binary bauen (Windows)
cd plugins/hello
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o plugin.exe .

# 2. Manifest signieren und Archiv bauen
cd ../../tools/build-plugin
go run . --manifest ../plugins/hello/manifest.json \
         --functions ../plugins/hello/functions.json \
         --readme ../plugins/hello/README.md \
         --binary ../plugins/hello/plugin.exe \
         --out hello-windows-amd64.glorious-plugin \
         --signing-key /pfad/zu/author_private.pem
```

Das Werkzeug gibt anschlie\u00dfend den `signer_pubkey` aus. Genau dieser Wert
(Base64 des Ed25519-Public-Keys) geh\u00f6rt in `index.json` und muss mit dem
`author_public.pem` des Zielsystems \u00fcbereinstimmen \u2014 sonst lehnt die Plattform
die Installation mit `manifest signature invalid` ab.

## Berechtigungen

Die `functions.json` deklariert genau ein Objekt `hello` mit einer Aktion
`execute`. Diese Aktion steht im System-Aktionsset der Plattform
(`read`, `create`, `update`, `delete`, `execute`, `manage`, `review`,
`grant`, `write`). Standardm\u00e4\u00dfig d\u00fcrfen Benutzer mit der Rolle `user` das
Objekt `hello` ausf\u00fchren (`execute`); Administratoren haben \u00fcber
`admin: ["*"]` vollen Zugriff.

Wichtig f\u00fcr den Lebenszyklus: Installieren, Starten und Stoppen eines Plugins
pr\u00fcft die Plattform gegen die deklarierten Objekte `install` bzw. `manage` mit
der Aktion `update` bzw. `manage`. Diese Aktionen h\u00e4lt nur die Rolle `admin`;
ein Plugin l\u00e4sst sich also nicht von einem normalen Benutzerkonto starten.
Nach der Installation registriert die Plattform zus\u00e4tzlich die Wildcard
`plugin.hello.*` f\u00fcr `admin`.

Zus\u00e4tzlich registriert das Plugin \u00fcber die Sektion `profile_fields` ein
Demo-Profilfeld (`favorite_color`, Typ `text`), das nach der Installation
als editierbares Feld im Benutzerprofil erscheint \u2014 ein Beispiel daf\u00fcr,
wie Plugins eigene Profilfelder beisteuern k\u00f6nnen.

Das Plugin selbst verlangt im `manifest.json` keine zus\u00e4tzlichen
Laufzeit-Permissions (`"permissions": []`) \u2014 es greift weder auf das
Netzwerk noch auf das Dateisystem au\u00dferhalb seines Arbeitsverzeichnisses
zu. Es lauscht ausschlie\u00dflich auf der Loopback-Adresse, die der Host
akzeptiert. Neue Plugins, die solche F\u00e4higkeiten ben\u00f6tigen, tragen sie hier
ein (z. B. `"permissions": ["network"]`).
