# hello — Vorlagen-Plugin

## Beschreibung

Das Plugin `hello` ist die offizielle Entwickler-Vorlage für Plugins im
Community-Repository `glorious-community-plugins`. Es zeigt die minimale,
lauffähige Struktur, die jedes Plugin mitbringen muss: ein Binary
(`main.go` + `go.mod`), ein `manifest.json` mit Metadaten, eine
`functions.json` als RBAC-Permission-Contract und diese README als
Pflicht-Dokumentation.

Das Binary ist eine bewusst einfache Dummy-Implementierung: Beim Start
loggt es `hello-plugin: started (version 0.1.0)`, liest anschließend
zeilenweise von stdin und antwortet auf die Eingabe `ping` mit `pong`.
Echte Plugins ersetzen diesen stdin/stdout-Ping-Pong durch die
net/rpc-basierte Plugin-API der Glorious Platform (siehe Kommentare in
`main.go` und die Plugin-API-Doku im Haupt-Repo).

Die ZIP-Artefakte (`.glorious-plugin`) werden nicht im Git-Repository
abgelegt, sondern über den Builder (`tools/build-plugin`) erzeugt und als
GitHub-Release-Assets veröffentlicht. Für den Build werden keine
speziellen Rechte benötigt — der Release-Workflow baut, validiert und
signiert das Plugin automatisch.

## Berechtigungen

Die `functions.json` deklariert genau ein Objekt `hello` mit einer Aktion
`execute`. Diese Aktion steht im System-Aktionsset der Plattform
(`read`, `create`, `update`, `delete`, `execute`, `manage`, `review`,
`grant`, `write`). Standardmäßig dürfen Benutzer mit der Rolle `user`
das Objekt `hello` ausführen (`execute`); Administratoren haben über
`admin: ["*"]` vollen Zugriff.

Das Plugin selbst verlangt im `manifest.json` keine zusätzlichen
Laufzeit-Permissions (`"permissions": []`) — es greift weder auf das
Netzwerk noch auf das Dateisystem außerhalb seines Arbeitsverzeichnisses
zu. Neue Plugins, die solche Fähigkeiten benötigen, tragen sie hier ein
(z. B. `"permissions": ["network"]`).
