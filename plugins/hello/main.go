// Command hello is the reference plugin of the Glorious Platform: a minimal
// but *real* out-of-process plugin.
//
// It is the working template any community plugin author can copy. The plugin
// runs as its own OS process, receives its mTLS identity through the
// GLORIOUS_PLUGIN_* environment, announces its net/rpc endpoint on stdout and
// then serves the handshake service the host requires before it hands over any
// traffic.
//
// Lifecycle, per the host contract (internal/plugins/runtime.go):
//
//  1. Read GLORIOUS_PLUGIN_NAME plus the identity material. On Windows — and on
//     any platform without descriptor inheritance — the environment carries
//     base64-encoded PEM in GLORIOUS_PLUGIN_CERT_PEM / GLORIOUS_PLUGIN_KEY_PEM;
//     on Unix those variables carry /proc/self/fd magic paths and the PEM
//     payload is read from the inherited descriptor instead. Both forms are
//     supported here.
//  2. Carry the CA certificate from GLORIOUS_PLUGIN_CA_PEM as the trust anchor.
//  3. Listen on 127.0.0.1:0 with a TLS listener that requires a client
//     certificate signed by that CA, never negotiating below the version
//     announced in GLORIOUS_PLUGIN_TLS_MIN_VERSION.
//  4. Flush the readiness line "GLO_PLUGIN_READY 127.0.0.1:<port>" to stdout
//     *before* serving. Without it the host waits out its 10s handshake timeout
//     and the plugin never starts.
//  5. Serve the handshake service (Handshake.Ping -> version "1.0") and the
//     plugin's own services over net/rpc.
//
// version must match manifest.json; rpcProtocolVersion is part of the host
// contract and must not be changed casually.
package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"log"
	"net/rpc"
	"os"
	"strings"
)

const (
	// version is logged at startup and must match manifest.json exactly.
	version = "0.1.2"
	// rpcProtocolVersion is the handshake protocol version the host requires
	// (internal/plugins/mtls.go PluginRPCVersion). A mismatch fails the start.
	rpcProtocolVersion = "1.0"
	// readyPrefix is the readiness announcement the host scans for on stdout;
	// the address after it must be a loopback host:port pair on an
	// unprivileged port (internal/plugins/runtime.go readReadyLine).
	readyPrefix = "GLO_PLUGIN_READY"
	// defaultListenAddr keeps the announced endpoint on loopback; the real
	// port is picked by the kernel.
	defaultListenAddr = "127.0.0.1:0"
)

// envPlugin* are the provisioning variables of the plugin SDK contract
// (internal/plugins/runtime.go). The namespace is reserved for the runtime.
const (
	envPluginName          = "GLORIOUS_PLUGIN_NAME"
	envPluginCertPEM       = "GLORIOUS_PLUGIN_CERT_PEM"
	envPluginKeyPEM        = "GLORIOUS_PLUGIN_KEY_PEM"
	envPluginCAPEM         = "GLORIOUS_PLUGIN_CA_PEM"
	envPluginTLSMinVersion = "GLORIOUS_PLUGIN_TLS_MIN_VERSION"
)

// handshakeService implements the host-mandated handshake: the host calls
// HandshakeService.Ping after the mTLS dial to verify liveness and the protocol
// version before it uses any of the plugin's own services.
//
// The wire name is part of the contract: net/rpc addresses a method as
// "<ServiceName>.<Method>", and the host's MTLSClient calls
// "HandshakeService.Ping" (internal/plugins/mtls.go). RegisterName therefore
// has to use that exact literal — a Go type name would only match by
// coincidence and a mismatch fails the start with "can't find service".
// The request and response structs keep the plain (unnamed) field encoding
// used by the host's HandshakeService.
type handshakeService struct{}

// PingRequest is the (empty) argument of Handshake.Ping.
type PingRequest struct{}

// PingResponse carries the protocol version spoken by this plugin.
type PingResponse struct {
	Version string
}

// Ping reports the RPC protocol version over the authenticated channel. The
// connection is already verified as the host by the TLS peer check, so the
// plugin answers with its version and nothing else.
func (handshakeService) Ping(_ PingRequest, resp *PingResponse) error {
	resp.Version = rpcProtocolVersion
	return nil
}

// helloService is the plugin's own demo service: functions.json declares the
// RBAC object "hello" with the single action "execute", so the service surface
// stays one read-only call.
type helloService struct {
	// name is the plugin name provisioned by the host; it lets a caller see
	// which instance answered.
	name string
}

// HelloRequest is the argument of Hello.Greet.
type HelloRequest struct {
	// Name is the display name the greeting addresses. Empty keeps the
	// greeting generic — the plugin never guesses an identity.
	Name string
}

// HelloResponse is the answer of Hello.Greet.
type HelloResponse struct {
	// Message is the rendered greeting.
	Message string
	// Plugin is the plugin instance name from GLORIOUS_PLUGIN_NAME.
	Plugin string
	// Version is the plugin's own version (see manifest.json).
	Version string
}

// Greet renders a greeting. It is the demo "execute" action of the plugin's
// declared object and stays side-effect free, so a grant of
// plugin.<name>.hello:execute can never mutate host state.
func (s helloService) Greet(req HelloRequest, resp *HelloResponse) error {
	who := strings.TrimSpace(req.Name)
	if who == "" {
		who = "world"
	}
	resp.Message = "hello, " + who
	resp.Plugin = s.name
	resp.Version = version
	return nil
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("hello-plugin: ")

	if err := run(); err != nil {
		log.Printf("startup failed: %v", err)
		os.Exit(1)
	}
}

// run provisions the identity, starts the mTLS RPC server, announces readiness
// and then serves until the host closes the channel or terminates the process.
// The split from main keeps every step testable and gives each failure a
// readable message on stderr.
func run() error {
	name := os.Getenv(envPluginName)
	if name == "" {
		return fmt.Errorf("environment %s is not set (the host provisions it)", envPluginName)
	}

	certPEM, keyPEM, err := loadIdentity()
	if err != nil {
		return err
	}
	keyPair, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return fmt.Errorf("invalid leaf certificate/key: %w", err)
	}

	caPEM, err := decodePEMEnv(envPluginCAPEM)
	if err != nil {
		return fmt.Errorf("CA certificate: %w", err)
	}
	caPool := x509.NewCertPool()
	if !caPool.AppendCertsFromPEM(caPEM) {
		return fmt.Errorf("environment %s carries no usable CA certificate", envPluginCAPEM)
	}

	// The host rejects peers whose leaf is not signed by the plugin CA, so the
	// plugin verifies the host certificate against the same trust anchor.
	tlsConf := &tls.Config{
		Certificates: []tls.Certificate{keyPair},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    caPool,
		MinVersion:   minTLSVersion(os.Getenv(envPluginTLSMinVersion)),
	}

	listener, err := tls.Listen("tcp", defaultListenAddr, tlsConf)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", defaultListenAddr, err)
	}
	defer func() { _ = listener.Close() }()

	server := rpc.NewServer()
	if err := server.RegisterName("HandshakeService", handshakeService{}); err != nil {
		return fmt.Errorf("register handshake service: %w", err)
	}
	if err := server.RegisterName("Hello", helloService{name: name}); err != nil {
		return fmt.Errorf("register hello service: %w", err)
	}

	// Readiness must reach the host before serving: its stdout scan is bounded
	// by a 10s timeout, and a plugin that never announces its address never
	// starts. os.Stdout is unbuffered, so this write is already on the pipe.
	log.Printf("ready (version %s, protocol %s)", version, rpcProtocolVersion)
	if _, err := fmt.Fprintf(os.Stdout, "%s %s\n", readyPrefix, listener.Addr().String()); err != nil {
		return fmt.Errorf("write readiness line: %w", err)
	}

	// rpc.Server.ServeConn is safe for concurrent use, so each accepted
	// connection gets its own goroutine. It speaks the gob wire format the
	// host's MTLSClient expects. The host stops the plugin by closing the
	// channel or terminating the process.
	for {
		conn, err := listener.Accept()
		if err != nil {
			return fmt.Errorf("accept: %w", err)
		}
		go server.ServeConn(conn)
	}
}

// loadIdentity resolves the leaf certificate and key from the environment. Two
// transport forms are part of the SDK contract: on Unix the PEM payload is
// attached to an inherited file descriptor and the variables carry the
// /proc/self/fd path the plugin reads; everywhere else — Windows has no
// descriptor inheritance — the variables carry base64-encoded PEM directly.
func loadIdentity() (certPEM, keyPEM []byte, err error) {
	certPEM, err = decodePEMEnv(envPluginCertPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("certificate: %w", err)
	}
	keyPEM, err = decodePEMEnv(envPluginKeyPEM)
	if err != nil {
		return nil, nil, fmt.Errorf("private key: %w", err)
	}
	if len(certPEM) == 0 || len(keyPEM) == 0 {
		return nil, nil, fmt.Errorf("environment %s/%s carry no identity material", envPluginCertPEM, envPluginKeyPEM)
	}
	return certPEM, keyPEM, nil
}

// decodePEMEnv reads one PEM-carrying environment variable. The value is
// either a file path (the Unix descriptor handoff uses /proc/self/fd/N, which
// is a plain readable path) or base64-encoded PEM. The newline guard on the
// base64 branch makes a PEM value that arrived unencoded fail loudly instead of
// being silently decoded into garbage.
func decodePEMEnv(key string) ([]byte, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil, nil
	}
	if strings.HasPrefix(value, "/") {
		data, err := os.ReadFile(value)
		if err != nil {
			return nil, fmt.Errorf("environment %s: read %s: %w", key, value, err)
		}
		return data, nil
	}
	if strings.ContainsAny(value, "\n\r") {
		return nil, fmt.Errorf("environment %s: value is neither a path nor base64-encoded PEM", key)
	}
	pem, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("environment %s: decode base64 PEM: %w", key, err)
	}
	return pem, nil
}

// minTLSVersion maps GLORIOUS_PLUGIN_TLS_MIN_VERSION ("1.2"/"1.3") to a
// crypto/tls constant. The host sets the floor on both channel ends; an unknown
// or missing value falls back to the host default of TLS 1.3, never to Go's
// own TLS 1.2 default.
func minTLSVersion(raw string) uint16 {
	switch strings.TrimSpace(raw) {
	case "1.2":
		return tls.VersionTLS12
	case "1.3", "":
		return tls.VersionTLS13
	default:
		log.Printf("unknown %s %q, using TLS 1.3", envPluginTLSMinVersion, raw)
		return tls.VersionTLS13
	}
}
