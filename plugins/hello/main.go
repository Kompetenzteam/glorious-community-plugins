// Command hello ist ein minimales Beispiel-Plugin für die Glorious Platform.
//
// Es dient als Entwickler-Vorlage: Es zeigt die minimale Struktur eines
// Plugin-Binaries — ein eigenständiges Programm, das die Plattform über den
// Entrypoint aus manifest.json startet. Die echte Plugin-API der Glorious
// Platform (net/rpc-basierte Kommunikation mit dem Core, Lifecycle-Hooks,
// RBAC-Registrierung über functions.json) ist in der Plugin-API-Doku im
// Haupt-Repo beschrieben (docs/…/docs/dev/plugins.md).
//
// Diese Dummy-Implementierung demonstriert den Lebenszyklus: Beim Start wird
// eine Log-Zeile ausgegeben, danach liest das Plugin zeilenweise von stdin
// und antwortet auf "ping" mit "pong" — ein Wegwerf-Beispiel, kein echter
// RPC-Handler.
package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strings"
)

// version wird beim Start geloggt und muss mit manifest.json übereinstimmen.
const version = "0.1.1"

func main() {
	log.Printf("hello-plugin: started (version %s)", version)

	// Zeilenweise von stdin lesen — die Plattform startet das Plugin als
	// Kindprozess; stdin/stdout sind hier die Demonstration der
	// Prozesskommunikation. Echte Plugins nutzen stattdessen net/rpc.
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		switch line {
		case "ping":
			fmt.Println("pong")
		default:
			log.Printf("hello-plugin: unbekannte Eingabe %q", line)
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("hello-plugin: stdin-Fehler: %v", err)
		os.Exit(1)
	}
}
