// EU AI Act transparency notice (Art. 50, Regulation (EU) 2024/1689): this
// source file was authored with AI assistance (backend-developer agent) as
// part of the hello 1.0.0 reference-plugin phase A work and is subject to the
// repository's standard human code review before release.
//
// Command hello is the reference plugin of the Glorious Platform.
//
// It is a complete, runnable reference implementation of the plugin contract
// (not a stub): the plugin starts an mTLS-secured net/rpc server on
// 127.0.0.1:0, registers the HandshakeService and announces its RPC address on
// stdout via the ready line. The host (internal/plugins/runtime.go) waits for
// exactly that line, then opens the mTLS connection and calls
// HandshakeService.Ping ("ping before use").
//
// # Contract (verified against glorious-platform_v2, internal/plugins)
//
//   - Listener: tls.Listen("tcp", "127.0.0.1:0", cfg) with
//     ClientAuth = RequireAndVerifyClientCert, MinVersion TLS 1.2
//     (mtls.go:320-330). GLORIOUS_PLUGIN_TLS_MIN_VERSION may demand 1.3.
//   - Trust anchor: CA pool from GLORIOUS_PLUGIN_CA_PEM (base64 PEM,
//     runtime.go:876).
//   - Leaf identity (runtime.go:72-77, runtime_unix.go:32-37,
//     runtime_windows.go): Windows transports cert/key as base64 PEM in
//     GLORIOUS_PLUGIN_CERT_PEM / GLORIOUS_PLUGIN_KEY_PEM; Unix carries the
//     paths /proc/self/fd/4 (cert) and /proc/self/fd/3 (key) in the same
//     variables, which the plugin reads as files. Both transports are
//     supported (see internal/config).
//   - Handshake: rpc.NewServer() + RegisterName("HandshakeService", ...)
//     (mtls.go:335). Ping(PingRequest, *PingResponse) sets resp.Version
//     (mtls.go:283-286) and is unchanged for backward compatibility. In
//     addition the same service answers the negotiated Handshake2 method
//     (see internal/handshake); the host is the CLIENT of Handshake2 and the
//     plugin is its server.
//   - Ready line on STDOUT, exactly: "GLO_PLUGIN_READY 127.0.0.1:<port>"
//     (pluginReadyPrefix == readyLinePrefix, runtime.go:94/162; read in
//     readReadyLine :1244, grammar in parseReadyLine :1069). Only
//     127.0.0.1/::1 and port 1024-65535 are accepted
//     (validatePluginListenAddr :899). The line is printed only after the
//     listener has started successfully.
//   - Fail-closed: when a required environment variable is missing, the
//     reason is logged (stderr) and the process exits with a non-zero code.
//     There are no defaults for certificate/key/CA material.
//
// # Second service
//
// ContributionService.DescribeContributions is defined in the host contract
// (internal/plugins/contract/handshake.go:113-138). This host version has no
// call site for it yet (the method set is "deliberately empty in W1" per the
// comment on lines 135-137). We still register the service with a
// contract-conformant implementation so the host can reach it unchanged once
// it uses it.
//
// # Messages
//
// Operator-facing messages are rendered in German or English via a small,
// dependency-free lookup (internal/i18n); the locale comes from
// GLORIOUS_PLUGIN_LOCALE and defaults to "de".
package main

import (
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/rpc"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"glorious-community/plugins/hello/internal/config"
	"glorious-community/plugins/hello/internal/handshake"
	"glorious-community/plugins/hello/internal/i18n"
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

// Environment variable names (owned by internal/config, which does the
// parsing). They are kept here as named aliases so package-main call sites
// read naturally; the values are identical to config's.
const (
	envCertPEM       = config.EnvCertPEM
	envKeyPEM        = config.EnvKeyPEM
	envCAPEM         = config.EnvCAPEM
	envTLSMinVersion = config.EnvTLSMinVersion
)

// tlsConfigFromEnv builds the server TLS configuration from the host-provided
// variables. It delegates to internal/config so the parsing rules live in one
// testable place; a missing required variable is an error (fail-closed).
func tlsConfigFromEnv() (*tls.Config, error) {
	return config.TLSFromEnv()
}

// tlsListen is a thin seam over tls.Listen so tests can observe the bind
// without reaching into the TLS configuration.
var tlsListen = func(cfg *tls.Config) (net.Listener, error) {
	return tls.Listen("tcp", "127.0.0.1:0", cfg)
}

// HandshakeService is the handshake the host calls before any use. The
// registered name must be EXACTLY "HandshakeService" (mtls.go:335). It carries
// both the legacy Ping (unchanged, backward compatible) and the negotiated
// Handshake2 method, whose implementation and request/reply contract live in
// internal/handshake and are promoted onto this service by embedding.
type HandshakeService struct {
	handshake.Service
}

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
	// Exact registration name per host contract (mtls.go:335). Register a
	// POINTER: Handshake2 is promoted from *handshake.Service, and net/rpc
	// only exposes pointer-receiver methods when the registered value is a
	// pointer (a value registration would silently omit Handshake2 while still
	// exposing the value-receiver Ping). HandshakeService embeds
	// handshake.Service, so both Ping and Handshake2 land on this one service.
	if err := rpcServer.RegisterName("HandshakeService", &HandshakeService{}); err != nil {
		return fmt.Errorf("register HandshakeService: %w", err)
	}
	if err := rpcServer.RegisterName("ContributionService", ContributionService{}); err != nil {
		return fmt.Errorf("register ContributionService: %w", err)
	}

	// tls.Listen binds 127.0.0.1:0 (mtls.go:327) — the concrete address is
	// announced right after.
	listener, err := tlsListen(cfg)
	if err != nil {
		return fmt.Errorf("mtls listen: %w", err)
	}
	defer listener.Close()

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return fmt.Errorf("unexpected listener type %T", listener.Addr())
	}
	if err := validateAnnouncedAddr(addr); err != nil {
		return err
	}

	// Only after the listener started successfully, print the ready line
	// (Spec Annex §11.1.1 step 5). Exactly one address, exact format.
	fmt.Printf("%s 127.0.0.1:%d\n", readyPrefix, addr.Port)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		serve(rpcServer, listener)
	}()

	// Ordered shutdown on SIGINT/SIGTERM (Unix). On Windows the host
	// terminates the process; the channel is still subscribed so later signal
	// support works without change.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Printf("%s", i18n.Message(i18n.KeyShutdown))
	_ = listener.Close()
	wg.Wait()
	return nil
}

// serve accepts mTLS connections and serves each in its own goroutine; a closed
// listener ends the loop.
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

// validateAnnouncedAddr ensures we only announce an address the host accepts
// (validatePluginListenAddr, runtime.go:899): loopback and port 1024-65535.
func validateAnnouncedAddr(addr *net.TCPAddr) error {
	if addr.IP == nil || !addr.IP.IsLoopback() {
		return fmt.Errorf("%s: %v", i18n.Message(i18n.KeyListenerRejected), addr.IP)
	}
	if addr.Port < 1024 || addr.Port > 65535 {
		return fmt.Errorf("%s: port %d outside 1024-65535", i18n.Message(i18n.KeyListenerRejected), addr.Port)
	}
	return nil
}
