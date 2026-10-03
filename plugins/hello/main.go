// Command hello ist das Vorlagen-Plugin der Glorious Platform.
//
// Es ist eine vollständige, lauffähige Referenz-Implementierung des
// Plugin-Vertrags (kein Dummy): Das Plugin startet einen mTLS-gesicherten
// net/rpc-Server auf 127.0.0.1:0, registriert den HandshakeService und
// meldet seine RPC-Adresse über die Ready-Zeile auf stdout. Der Host
// (internal/plugins/runtime.go) wartet auf genau diese Zeile, baut dann die
// mTLS-Verbindung auf und ruft HandshakeService.Ping auf ("Ping-vor-Nutzung").
//
// # Vertrag (verifiziert gegen glorious-platform_v2, internal/plugins)
//
//   - Listener: tls.Listen("tcp", "127.0.0.1:0", cfg) mit
//     ClientAuth = RequireAndVerifyClientCert, MinVersion TLS 1.2
//     (mtls.go:320-330). GLORIOUS_PLUGIN_TLS_MIN_VERSION kann 1.3 fordern.
//   - Trust anchor: CA-Pool aus GLORIOUS_PLUGIN_CA_PEM (base64-PEM,
//     runtime.go:876).
//   - Leaf-Identität (runtime.go:72-77, runtime_unix.go:32-37,
//     runtime_windows.go): Windows transportiert Cert/Key als base64-PEM in
//     GLORIOUS_PLUGIN_CERT_PEM / GLORIOUS_PLUGIN_KEY_PEM; Unix trägt in
//     denselben Variablen die Pfade /proc/self/fd/4 (Cert) und
//     /proc/self/fd/3 (Key), die das Plugin als Datei liest. Beide
//     Transporte werden hier unterstützt.
//   - Handshake: rpc.NewServer() + RegisterName("HandshakeService", ...)
//     (mtls.go:335). HandshakeService.Ping(PingRequest, *PingResponse) setzt
//     resp.Version (mtls.go:283-286). Der Host akzeptiert die Versionen aus
//     contract.AcceptedRPCVersions ("1.0", "1.1"); wir melden "1.0"
//     (PluginRPCVersion, mtls.go:34) — die minimale, immer akzeptierte
//     Protokollversion. Die Handshake2-Negotiation (1.1) ist optional und
//     wird vom Host erst nach explizitem Opt-in genutzt.
//   - Ready-Zeile auf STDOUT, exakt: "GLO_PLUGIN_READY 127.0.0.1:<port>"
//     (pluginReadyPrefix == readyLinePrefix, runtime.go:94/162; gelesen in
//     readReadyLine :1244, Grammatik in parseReadyLine :1069). Nur
//     127.0.0.1/::1 und Port 1024-65535 werden akzeptiert
//     (validatePluginListenAddr :899). Die Zeile wird erst nach dem
//     erfolgreichen Start des Listeners gedruckt.
//   - Fail-closed: fehlt eine benötigte Env-Variable, wird das klar
//     geloggt (stderr) und der Prozess beendet sich mit Exit-Code != 0.
//     Es gibt keine Defaults für Zertifikat/Schlüssel/CA.
//
// # Zweiter Service
//
// ContributionService.DescribeContributions ist im Host-Contract definiert
// (internal/plugins/contract/handshake.go:113-138). Der Host hat in dieser
// Version noch keine Aufrufstelle dafür (der Methodensatz ist laut Kommentar
// Zeile 135-137 "deliberately empty in W1"). Wir registrieren den Service
// dennoch mit einer vertragskonformen Implementierung, damit der Host ihn
// ohne Änderung erreichen kann, sobald er ihn nutzt.
package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/rpc"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
)

// rpcVersion ist die vom Plugin gemeldete RPC-Protokollversion. Sie muss in
// contract.AcceptedRPCVersions ("1.0", "1.1") liegen; "1.0" ist die
// Basis-Version des Vertrags (host-seitig PluginRPCVersion, mtls.go:34).
const rpcVersion = "1.0"

// readyPrefix ist der Ready-Marker auf stdout (host-seitig readyLinePrefix,
// runtime.go:94). Format der Zeile: "<readyPrefix> 127.0.0.1:<port>".
const readyPrefix = "GLO_PLUGIN_READY"

// contributionVersion ist die Contribution-Contract-Version
// (contract.PluginContributionVersion).
const contributionVersion = "1.0.0"

// Env-Vertrag (Spiegel von internal/plugins/runtime.go:72-77).
const (
	envCertPEM       = "GLORIOUS_PLUGIN_CERT_PEM"
	envKeyPEM        = "GLORIOUS_PLUGIN_KEY_PEM"
	envCAPEM         = "GLORIOUS_PLUGIN_CA_PEM"
	envTLSMinVersion = "GLORIOUS_PLUGIN_TLS_MIN_VERSION"
)

// HandshakeService ist der Ping-Handshake, den der Host vor jeder Nutzung
// aufruft. Der registrierte Name muss EXAKT "HandshakeService" lauten
// (mtls.go:335).
type HandshakeService struct{}

// PingRequest ist das leere Argument des Handshake-Ping.
type PingRequest struct{}

// PingResponse trägt die RPC-Protokollversion des Plugins.
type PingResponse struct {
	Version string
}

// Ping antwortet mit der gesprochenen Protokollversion.
func (HandshakeService) Ping(_ PingRequest, resp *PingResponse) error {
	resp.Version = rpcVersion
	return nil
}

// Contribution spiegelt contract.Contribution (handshake.go:100-111).
type Contribution struct {
	ID          string
	Kind        string
	Name        string
	Description string
}

// DescribeContributionsRequest ist das leere Argument (handshake.go:117).
type DescribeContributionsRequest struct{}

// DescribeContributionsReply spiegelt contract.DescribeContributionsReply
// (handshake.go:123-131).
type DescribeContributionsReply struct {
	Contributions       []Contribution
	ContributionVersion string
}

// ContributionService ist der zweite erreichbare Service des Plugins.
type ContributionService struct{}

// DescribeContributions liefert die Contributions des Plugins. Die Vorlage
// bietet genau eine Task-Contribution an, die dokumentiert, dass der
// Service erreichbar ist.
func (ContributionService) DescribeContributions(_ DescribeContributionsRequest, reply *DescribeContributionsReply) error {
	reply.Contributions = []Contribution{
		{
			ID:          "hello-handshake",
			Kind:        "task",
			Name:        "Hello Handshake",
			Description: "Vorlagen-Contribution des hello-Plugins: demonstriert den ContributionService über RPC.",
		},
	}
	reply.ContributionVersion = contributionVersion
	return nil
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("hello-plugin: ")
	if err := run(); err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}
}

// run kapselt Start und Serve-Lebenszyklus (testbar ohne os.Exit).
func run() error {
	flag.Parse()

	cfg, err := tlsConfigFromEnv()
	if err != nil {
		return err // fail-closed: keine Defaults
	}

	rpcServer := rpc.NewServer()
	// Exakter Registrierungsname laut Host-Vertrag (mtls.go:335).
	if err := rpcServer.RegisterName("HandshakeService", HandshakeService{}); err != nil {
		return fmt.Errorf("register HandshakeService: %w", err)
	}
	if err := rpcServer.RegisterName("ContributionService", ContributionService{}); err != nil {
		return fmt.Errorf("register ContributionService: %w", err)
	}

	// tls.Listen bindet 127.0.0.1:0 (mtls.go:327) — die konkrete Adresse
	// wird gleich bekanntgegeben.
	listener, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
	if err != nil {
		return fmt.Errorf("mtls listen: %w", err)
	}
	defer listener.Close()

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("unerwarteter Listener-Typ %T", listener.Addr())
	}
	if err := validateAnnouncedAddr(addr); err != nil {
		return err
	}

	// Erst nach erfolgreichem Start des Listeners die Ready-Zeile drucken
	// (Spec Annex §11.1.1 Schritt 5). Genau eine Adresse, exaktes Format.
	fmt.Printf("%s 127.0.0.1:%d\n", readyPrefix, addr.Port)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		serve(rpcServer, listener)
	}()

	// Geordnetes Herunterfahren bei SIGINT/SIGTERM (Unix). Auf Windows
	// terminiert der Host den Prozess; der Kanal wird trotzdem abonniert,
	// damit ein späterer Support der Signale ohne Änderung greift.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Printf("shutdown signal empfangen, beende")
	_ = listener.Close()
	wg.Wait()
	return nil
}

// serve nimmt mTLS-Verbindungen an und bedient jede in einer eigenen
// Goroutine; ein geschlossener Listener beendet die Schleife.
func serve(srv *rpc.Server, listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			log.Printf("accept: %v", err)
			return
		}
		go srv.ServeConn(conn)
	}
}

// validateAnnouncedAddr stellt sicher, dass wir nur eine Adresse ankündigen,
// die der Host akzeptiert (validatePluginListenAddr, runtime.go:899):
// loopback und Port 1024-65535.
func validateAnnouncedAddr(addr *net.TCPAddr) error {
	if addr.IP == nil || !addr.IP.IsLoopback() {
		return fmt.Errorf("listener nicht auf loopback (%v)", addr.IP)
	}
	if addr.Port < 1024 || addr.Port > 65535 {
		return fmt.Errorf("listener-Port %d ausserhalb 1024-65535", addr.Port)
	}
	return nil
}

// tlsConfigFromEnv baut die Server-TLS-Konfiguration ausschließlich aus den
// vom Host provisionierten Variablen. Fehlt eine benötigte Variable, wird
// ein Fehler zurückgegeben (fail-closed, keine Defaults).
func tlsConfigFromEnv() (*tls.Config, error) {
	certPEM, err := leafMaterial(envCertPEM)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", envCertPEM, err)
	}
	keyPEM, err := leafMaterial(envKeyPEM)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", envKeyPEM, err)
	}
	caPEM, err := caMaterial()
	if err != nil {
		return nil, err
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("leaf keypair: %w", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, errors.New("CA-Pool: kein Zertifikat aus GLORIOUS_PLUGIN_CA_PEM lesbar")
	}

	minVersion, err := tlsMinVersion()
	if err != nil {
		return nil, err
	}

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    pool,
		MinVersion:   minVersion,
	}, nil
}

// leafMaterial liest das Blattmaterial aus dem Transport, den der Host
// plattformabhängig wählt. Der Wert ist entweder ein Pfad (Unix:
// /proc/self/fd/3 bzw. /proc/self/fd/4, dann wird die Datei gelesen) oder
// base64-kodiertes PEM (Windows). Beide Formen werden unterstützt; eine
// fehlende Variable ist ein Fehler (fail-closed).
func leafMaterial(envKey string) ([]byte, error) {
	raw, ok := os.LookupEnv(envKey)
	if !ok || strings.TrimSpace(raw) == "" {
		return nil, errors.New("Umgebungsvariable fehlt")
	}
	return decodeLeafValue(raw)
}

// decodeLeafValue unterscheidet die beiden Transportformen: beginnt der Wert
// mit "/" oder "./" (oder existiert schlicht als Datei), wird er als Pfad
// behandelt (Unix-FD-Weg); sonst als base64-PEM (Windows-Weg).
func decodeLeafValue(raw string) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "./") {
		data, err := os.ReadFile(trimmed)
		if err != nil {
			return nil, fmt.Errorf("PEM-Datei %q lesen: %w", trimmed, err)
		}
		return data, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		// Manche Setups liefern PEM im Klartext statt base64 — akzeptieren,
		// aber nur wenn es tatsächlich ein PEM-Block ist.
		if block, _ := pem.Decode([]byte(trimmed)); block != nil {
			return []byte(trimmed), nil
		}
		return nil, fmt.Errorf("Wert ist weder Dateipfad noch base64-PEM: %w", err)
	}
	return decoded, nil
}

// caMaterial liest den CA-Trust-Anchor aus GLORIOUS_PLUGIN_CA_PEM
// (base64-PEM, runtime.go:876). Keine Defaults.
func caMaterial() ([]byte, error) {
	raw, ok := os.LookupEnv(envCAPEM)
	if !ok || strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("%s: Umgebungsvariable fehlt", envCAPEM)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("%s: base64-Dekodierung fehlgeschlagen: %w", envCAPEM, err)
	}
	return decoded, nil
}

// tlsMinVersion liest GLORIOUS_PLUGIN_TLS_MIN_VERSION ("1.2"/"1.3"). Fehlt
// die Variable, gilt TLS 1.2 als Host-Default (mtls.go:324). Ein unbekannter
// Wert ist ein Fehler (fail-closed statt stiller Downgrade).
func tlsMinVersion() (uint16, error) {
	raw, ok := os.LookupEnv(envTLSMinVersion)
	if !ok || strings.TrimSpace(raw) == "" {
		return tls.VersionTLS12, nil
	}
	switch strings.TrimSpace(raw) {
	case "1.2":
		return tls.VersionTLS12, nil
	case "1.3":
		return tls.VersionTLS13, nil
	default:
		return 0, fmt.Errorf("%s: unbekannter Wert %q (erlaubt: 1.2, 1.3)", envTLSMinVersion, raw)
	}
}
