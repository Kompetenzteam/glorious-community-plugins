// Command routeexample is a complete, runnable reference plugin for the
// Glorious Platform that demonstrates the v1.1 contribution surface end to
// end: it announces RPC protocol version "1.1" through the mandatory
// HandshakeService, answers the host's negotiated ContributionService.Handshake2,
// declares one HTTP route (GET /hello) through
// ContributionService.DescribeContributions and serves that route through
// WebService.ServeHTTP over the same mTLS-encrypted net/rpc channel.
//
// It exists to unblock the acceptance E2E's last missing step (the HTTP
// passthrough): the host gates the whole contribution path fail-closed behind
// the v1.1 negotiation, so a plugin that only reports "1.0" (like the hello
// template) never gets asked for routes and mounts nothing. This plugin is
// the minimal, honest example of a plugin that DOES speak 1.1 and DOES
// declare a route.
//
// # Contract (verified against glorious-platform_v2 internal/plugins)
//
//   - Listener: tls.Listen("tcp", "127.0.0.1:0", cfg) with
//     ClientAuth = RequireAndVerifyClientCert (mtls.go).
//   - Trust anchor: CA pool from GLORIOUS_PLUGIN_CA_PEM (base64-PEM).
//   - Leaf identity: Windows transports cert/key as base64-PEM in
//     GLORIOUS_PLUGIN_CERT_PEM / GLORIOUS_PLUGIN_KEY_PEM; Unix carries the
//     paths /proc/self/fd/4 (cert) and /proc/self/fd/3 (key) in the same
//     variables. Both transports are supported here.
//   - Readiness line on STDOUT, exactly: "GLO_PLUGIN_READY 127.0.0.1:<port>",
//     printed only after the listener is up. Only loopback and ports
//     1024-65535 are accepted by the host.
//   - HandshakeService.Ping must report "1.1" (not the frozen "1.0" default):
//     the host only proceeds to ContributionService.Handshake2 when the Ping
//     announced a contribution-capable version (runtime.go, "a \"1.0\" plugin
//     gets ZERO Handshake2 calls").
//   - ContributionService.Handshake2 answers with PluginRPCVersion "1.1", the
//     addressed Name, InstanceID "gsc" and the "routes" feature; the host
//     rejects any other version fail-closed (ErrUnsupportedContributionVersion).
//   - ContributionService.DescribeContributions returns the static route list
//     (contract.DescribeResp.Routes []RouteSpec).
//   - WebService.ServeHTTP carries one forwarded HTTP request and answers with
//     the HTTPResp the host writes back to the client.
//
// net/rpc encodes structs by FIELD NAME (gob), and the community repo cannot
// import glorious-platform-v2/internal/... (different module, internal
// package). Every wire struct below therefore mirrors the host contract's
// exported field names and order character-for-character; a drift breaks the
// host decode at runtime, which the local contract test pins by reflection.
//
// EU AI Act, Art. 50 (Regulation (EU) 2024/1689): this source file was
// authored with AI assistance and is subject to human review before release.
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

// Version constants of the plugin wire contract.
const (
	// rpcVersion is the RPC protocol version announced through
	// HandshakeService.Ping. It MUST be "1.1": only then does the host open the
	// contribution surface (Handshake2/DescribeContributions). "1.0" is the
	// frozen legacy default that gates every plugin off.
	rpcVersion = "1.1"
	// contributionVersion is the contribution contract version
	// (contract.PluginContributionVersion == "1.1").
	contributionVersion = "1.1"
	// instanceIDV11 is the sole v1.1 instance namespace
	// (contract.InstanceIDV11 == "gsc").
	instanceIDV11 = "gsc"
	// featureRoutes marks a plugin that contributes HTTP routes
	// (contract.FeatureRoutes).
	featureRoutes = "routes"
	// serverVersion is the human-readable plugin build version reported in
	// Handshake2Resp.ServerVersion (diagnostics only).
	serverVersion = "0.1.0"

	// handshakeServiceName / contributionServiceName / webServiceName are the
	// net/rpc service names the host builds its method calls from. They are
	// exact strings (contract.WebServiceName == "WebService"); a mismatch
	// yields "rpc: can't find service" on the host.
	handshakeServiceName    = "HandshakeService"
	contributionServiceName = "ContributionService"
	webServiceName          = "WebService"
)

// Routing constants of the one route this example declares.
const (
	// routePath is the plugin-relative path the host maps under
	// /plugins/<name>/. It must start with "/".
	routePath = "/hello"
	// routeObject is the RBAC object the route is bound to. The host enforces
	// that a non-public route's Object sits inside the owning plugin's own
	// namespace and starts with "plugin.<pluginName>." followed by a non-empty
	// segment (internal/plugins/gateway/gateway.go ValidateRouteSpec); the RBAC
	// loader assembles this name as "plugin." + pluginName + "." + obj.Name
	// from functions.json (internal/plugins/rbac_loader.go). So the bound object
	// is "plugin.routeexample.routeexample" for the bare functions.json object
	// name "routeexample".
	routeObject = "plugin.routeexample.routeexample"
	// routeAction is the RBAC action the route requires.
	routeAction = "read"
	// routeBody is the fixed response body, so the acceptance E2E can assert a
	// plugin-authored payload travelled the full chain.
	routeBody = "routeexample-ok"
)

// readyPrefix is the readiness marker on stdout (host-side
// plugins.pluginReadyPrefix). Line format: "<readyPrefix> 127.0.0.1:<port>".
const readyPrefix = "GLO_PLUGIN_READY"

// Environment contract (mirror of internal/plugins/runtime.go).
const (
	envCertPEM       = "GLORIOUS_PLUGIN_CERT_PEM"
	envKeyPEM        = "GLORIOUS_PLUGIN_KEY_PEM"
	envCAPEM         = "GLORIOUS_PLUGIN_CA_PEM"
	envTLSMinVersion = "GLORIOUS_PLUGIN_TLS_MIN_VERSION"
)

// HandshakeService is the mandatory handshake the host calls before any use.
// The registered name must be exactly "HandshakeService".
type HandshakeService struct{}

// PingRequest is the empty argument of the handshake Ping. Its name and
// emptiness mirror plugins.PingRequest; net/rpc compares the method signature
// name, and an empty struct encodes identically on both sides.
type PingRequest struct{}

// PingResponse carries the plugin's RPC protocol version. The field name
// "Version" is the wire name (gob) the host decodes into
// plugins.PingResponse.Version.
type PingResponse struct {
	Version string
}

// Ping answers with the spoken protocol version. It returns "1.1" so the host
// proceeds to the negotiated Handshake2 and the contribution mount; a "1.0"
// answer would leave the contribution surface closed.
func (HandshakeService) Ping(_ PingRequest, resp *PingResponse) error {
	resp.Version = rpcVersion
	return nil
}

// Features mirrors contract.Features ([]string). The host only interprets the
// tokens it knows, so the alias is wire-identical.
type Features []string

// Handshake2Req mirrors contract.Handshake2Req (the HOST -> PLUGIN negotiated
// handshake). Field names and order are the gob wire contract.
type Handshake2Req struct {
	HostRPCVersion string
	HostFeatures   Features
	PluginName     string
	InstanceID     string
}

// Handshake2Resp mirrors contract.Handshake2Resp. PluginRPCVersion is the
// authoritative gate: the host accepts only "1.1" and discards the reply
// otherwise (fail-closed); Name and InstanceID bind the reply to the
// addressed plugin (M-12 identity rule).
type Handshake2Resp struct {
	PluginRPCVersion        string
	Features                Features
	Name                    string
	InstanceID              string
	AcceptedProtocolVersion string
	ContributionVersion     string
	ServerVersion           string
}

// ContributionService is the v1.1 contribution surface the host reaches after
// the Ping. The registered name must be exactly "ContributionService".
type ContributionService struct {
	// manifestName is the plugin's manifest name. It is echoed in
	// Handshake2Resp.Name and used to reject a handshake addressed to a
	// different plugin. It is set by the process from the manifest and is not
	// a wire field.
	manifestName string
}

// Handshake2 answers the host's negotiated handshake. It reports the plugin's
// RPC version "1.1" (the gate the host checks first), echoes the addressed
// plugin identity, advertises the "routes" feature and states the contribution
// version. A request addressed to another plugin is refused (fail-closed).
func (c ContributionService) Handshake2(req Handshake2Req, resp *Handshake2Resp) error {
	if req.PluginName != "" && req.PluginName != c.manifestName {
		return fmt.Errorf("handshake2: addressed to %q, this plugin is %q", req.PluginName, c.manifestName)
	}
	resp.PluginRPCVersion = rpcVersion
	resp.Features = Features{featureRoutes}
	resp.Name = c.manifestName
	resp.InstanceID = instanceIDV11
	resp.AcceptedProtocolVersion = rpcVersion
	resp.ContributionVersion = contributionVersion
	resp.ServerVersion = serverVersion
	return nil
}

// RouteSpec mirrors contract.RouteSpec: one HTTP route declaration. Method
// empty means GET; Path is relative to the mount prefix and must start with
// "/". Object/Action bind the route to the RBAC object from functions.json.
type RouteSpec struct {
	Method       string
	Path         string
	Object       string
	Action       string
	Public       bool
	Streaming    bool
	MaxBodyBytes int64
}

// DescribeContributionsRequest mirrors contract.DescribeContributionsRequest
// (empty argument).
type DescribeContributionsRequest struct{}

// DescribeResp mirrors contract.DescribeResp: the plugin's static contribution
// list. Routes is non-empty here — that is exactly what unblocks the HTTP E2E.
// A non-empty ErrorCode would make the host fail the mount (fail-closed), so
// this example never sets one on success.
type DescribeResp struct {
	Routes    []RouteSpec
	Templates []TemplateSpec
	Assets    []AssetSpec
	ErrorCode string
}

// TemplateSpec mirrors contract.TemplateSpec (unused here; kept so the wire
// struct field set matches if the host ever round-trips a reply).
type TemplateSpec struct {
	Name   string
	Layout string
}

// AssetSpec mirrors contract.AssetSpec (unused here).
type AssetSpec struct {
	RelPath string
	SHA256  string
}

// DescribeContributions returns the plugin's static contribution list: exactly
// one non-public GET route. The host validates it, registers it as an RBAC
// rule and mounts it under /plugins/<name>/.
func (ContributionService) DescribeContributions(_ DescribeContributionsRequest, resp *DescribeResp) error {
	resp.Routes = []RouteSpec{
		{
			Method: "GET",
			Path:   routePath,
			Object: routeObject,
			Action: routeAction,
		},
	}
	return nil
}

// HTTPReq mirrors contract.HTTPReq: one forwarded HTTP request on its way to
// the plugin. The host strips hop-by-hop and security-critical headers first,
// so a plugin never sees a Cookie, Authorization or CSRF token here.
type HTTPReq struct {
	Method     string
	Path       string
	Query      string
	Header     map[string][]string
	Body       []byte
	PluginName string
	InstanceID string
	Principal  UserPrincipal
	RequestID  string
	CSRFOK     bool
}

// UserPrincipal mirrors contract.UserPrincipal (host identity context,
// read-only for the plugin).
type UserPrincipal struct {
	UserID   string
	Username string
	Roles    []string
	Lang     string
}

// HTTPResp mirrors contract.HTTPResp: the plugin's answer to one forwarded
// request. The host filters the headers down to a fixed allowlist, so this
// example sets only Content-Type. StreamNext false (no streaming in v1.1).
type HTTPResp struct {
	Status     int
	Header     map[string][]string
	Body       []byte
	StreamNext bool
	ErrorCode  string
}

// WebService is the HTTP transport surface. The host's mount forwards a
// dispatched request to WebService.ServeHTTP over the established mTLS
// channel; the plugin answers with the HTTPResp written back to the client.
// The registered name must be exactly "WebService".
type WebService struct{}

// ServeHTTP answers one forwarded request. It returns the fixed route body for
// the declared GET /hello route and a clean 404 for anything else, so an
// undeclared path never masquerades as success (defense in depth: the host
// already default-denies undeclared paths before forwarding).
func (WebService) ServeHTTP(req HTTPReq, resp *HTTPResp) error {
	if req.Method != "GET" || req.Path != routePath {
		resp.Status = 404
		resp.Header = map[string][]string{"Content-Type": {"text/plain; charset=utf-8"}}
		resp.Body = []byte("not found")
		return nil
	}
	resp.Status = 200
	resp.Header = map[string][]string{"Content-Type": {"text/plain; charset=utf-8"}}
	resp.Body = []byte(routeBody)
	return nil
}

// manifestName is the plugin's identity. It equals the manifest.json "name"
// field; the host addresses the plugin by it and the Handshake2 identity check
// compares against it exactly.
const manifestName = "routeexample"

func main() {
	log.SetFlags(0)
	log.SetPrefix("routeexample: ")
	if err := run(); err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}
}

// run encapsulates start and serve lifecycle (testable without os.Exit).
func run() error {
	flag.Parse()

	cfg, err := tlsConfigFromEnv()
	if err != nil {
		return err // fail-closed: no defaults
	}

	rpcServer := rpc.NewServer()
	// Exact registration names per the host wire contract.
	if err := rpcServer.RegisterName(handshakeServiceName, HandshakeService{}); err != nil {
		return fmt.Errorf("register %s: %w", handshakeServiceName, err)
	}
	if err := rpcServer.RegisterName(contributionServiceName, ContributionService{manifestName: manifestName}); err != nil {
		return fmt.Errorf("register %s: %w", contributionServiceName, err)
	}
	if err := rpcServer.RegisterName(webServiceName, WebService{}); err != nil {
		return fmt.Errorf("register %s: %w", webServiceName, err)
	}

	listener, err := tls.Listen("tcp", "127.0.0.1:0", cfg)
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

	// Print the readiness line only after the listener is up, exactly once,
	// with the exact format the host parses.
	fmt.Printf("%s 127.0.0.1:%d\n", readyPrefix, addr.Port)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		serve(rpcServer, listener)
	}()

	// Graceful shutdown on SIGINT/SIGTERM (Unix). On Windows the host
	// terminates the process; the channel is subscribed anyway so a later
	// signal support needs no change.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Printf("shutdown signal received, exiting")
	_ = listener.Close()
	wg.Wait()
	return nil
}

// serve accepts mTLS connections and serves each in its own goroutine; a
// closed listener ends the loop.
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

// validateAnnouncedAddr ensures only an address the host accepts is announced
// (plugins.validatePluginListenAddr): loopback and port 1024-65535.
func validateAnnouncedAddr(addr *net.TCPAddr) error {
	if addr.IP == nil || !addr.IP.IsLoopback() {
		return fmt.Errorf("listener not on loopback (%v)", addr.IP)
	}
	if addr.Port < 1024 || addr.Port > 65535 {
		return fmt.Errorf("listener port %d outside 1024-65535", addr.Port)
	}
	return nil
}

// tlsConfigFromEnv builds the server TLS configuration solely from the
// host-provisioned variables. A missing variable is an error (fail-closed, no
// defaults).
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
		return nil, errors.New("CA pool: no certificate readable from GLORIOUS_PLUGIN_CA_PEM")
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

// leafMaterial reads leaf material from the transport the host chooses. The
// value is either a path (Unix: /proc/self/fd/3 or /proc/self/fd/4, then the
// file is read) or base64-encoded PEM (Windows). Both forms are supported; a
// missing variable is an error (fail-closed).
func leafMaterial(envKey string) ([]byte, error) {
	raw, ok := os.LookupEnv(envKey)
	if !ok || strings.TrimSpace(raw) == "" {
		return nil, errors.New("environment variable missing")
	}
	return decodeLeafValue(raw)
}

// decodeLeafValue distinguishes the two transport forms: a value beginning
// with "/" or "./" is treated as a path (Unix FD transport); otherwise as
// base64-PEM (Windows transport).
func decodeLeafValue(raw string) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "./") {
		data, err := os.ReadFile(trimmed)
		if err != nil {
			return nil, fmt.Errorf("read PEM file %q: %w", trimmed, err)
		}
		return data, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil {
		// Some setups deliver plain PEM instead of base64 — accept it, but
		// only if it really is a PEM block.
		if block, _ := pem.Decode([]byte(trimmed)); block != nil {
			return []byte(trimmed), nil
		}
		return nil, fmt.Errorf("value is neither a file path nor base64 PEM: %w", err)
	}
	return decoded, nil
}

// caMaterial reads the CA trust anchor from GLORIOUS_PLUGIN_CA_PEM
// (base64-PEM). No defaults.
func caMaterial() ([]byte, error) {
	raw, ok := os.LookupEnv(envCAPEM)
	if !ok || strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("%s: environment variable missing", envCAPEM)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("%s: base64 decode failed: %w", envCAPEM, err)
	}
	return decoded, nil
}

// tlsMinVersion reads GLORIOUS_PLUGIN_TLS_MIN_VERSION ("1.2"/"1.3"). Missing
// means TLS 1.2 (host default). An unknown value is an error (fail-closed
// rather than a silent downgrade).
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
		return 0, fmt.Errorf("%s: unknown value %q (allowed: 1.2, 1.3)", envTLSMinVersion, raw)
	}
}
