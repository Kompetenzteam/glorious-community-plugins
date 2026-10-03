// EU AI Act transparency notice (Art. 50, Regulation (EU) 2024/1689): this
// source file was authored with AI assistance (backend-developer agent) as
// part of the hello 1.0.0 reference-plugin phase A work and is subject to the
// repository's standard human code review before release.
package main

import (
	"crypto/tls"
	"net/rpc"
	"reflect"
	"testing"

	"glorious-community/plugins/hello/internal/handshake"
)

// expectedFeatures is the independently authored expected feature set for the
// reference plugin, hard-coded in the test on purpose. It must NOT be derived
// from handshake.Features: comparing the wire reply against the production
// variable would be a self-comparison tautology that stays green even when a
// feature is dropped from handshake.Features. Listing the tokens literally
// here forces any change to the advertised surface to show up as a red test.
var expectedFeatures = []string{"routes", "jobs", "models", "nav"}

// dialHost opens a real mutual-TLS connection to the running plugin as the host
// would, returning an rpc.Client. It mirrors the handshake test's dial.
func dialHost(t *testing.T, ca *testCA, addr string) *rpc.Client {
	t.Helper()
	conn, err := tls.Dial("tcp", addr, &tls.Config{
		Certificates: []tls.Certificate{ca.clientCr},
		RootCAs:      ca.pool,
		ServerName:   "hello-plugin",
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("mTLS dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return rpc.NewClient(conn)
}

// TestHandshake2OverMTLS exercises the negotiated handshake end to end over the
// real wire: the plugin must expose HandshakeService.Handshake2 on the same
// registered service as Ping, and the reply fields must line up by name (gob).
func TestHandshake2OverMTLS(t *testing.T) {
	ca := newTestCA(t)
	ca.setEnv(t, true, true, true)

	addr, cleanup := startPlugin(t)
	defer cleanup()

	client := dialHost(t, ca, addr)

	var reply handshake.Handshake2Reply
	req := handshake.Handshake2Request{
		HostRPCVersion: "1.1",
		HostFeatures:   []string{"routes", "jobs", "models", "nav"},
		PluginName:     "hello",
		InstanceID:     "",
	}
	if err := client.Call("HandshakeService.Handshake2", req, &reply); err != nil {
		t.Fatalf("HandshakeService.Handshake2: %v", err)
	}
	if reply.PluginRPCVersion != handshake.PluginRPCVersion11 {
		t.Errorf("PluginRPCVersion = %q, want %q", reply.PluginRPCVersion, handshake.PluginRPCVersion11)
	}
	if reply.Name != "hello" {
		t.Errorf("Name = %q, want \"hello\"", reply.Name)
	}
	if reply.ErrorCode != "" {
		t.Errorf("ErrorCode = %q, want empty", reply.ErrorCode)
	}
	if !reflect.DeepEqual(reply.Features, expectedFeatures) {
		t.Errorf("Features = %v, want %v (independent expectation, not handshake.Features)", reply.Features, expectedFeatures)
	}
}

// TestHandshake2WrongNameOverMTLS verifies the mismatch path travels over the
// wire: the RPC call fails. NOTE: net/rpc discards the reply body when the
// method returns a non-nil error, so the client observes the error channel
// here; the reply's ErrorCode field is asserted at the unit level
// (internal/handshake) where the reply is returned directly.
func TestHandshake2WrongNameOverMTLS(t *testing.T) {
	ca := newTestCA(t)
	ca.setEnv(t, true, true, true)

	addr, cleanup := startPlugin(t)
	defer cleanup()

	client := dialHost(t, ca, addr)

	var reply handshake.Handshake2Reply
	req := handshake.Handshake2Request{PluginName: "not-hello"}
	if err := client.Call("HandshakeService.Handshake2", req, &reply); err == nil {
		t.Fatal("expected error for wrong plugin name over the wire")
	}
}

// TestHandshakePingStillOneZero is a regression guard for backward
// compatibility: the legacy Ping must keep reporting "1.0" even though
// Handshake2 now advertises "1.1", and both must live on one service.
func TestHandshakePingStillOneZero(t *testing.T) {
	ca := newTestCA(t)
	ca.setEnv(t, true, true, true)

	addr, cleanup := startPlugin(t)
	defer cleanup()

	client := dialHost(t, ca, addr)

	var ping PingResponse
	if err := client.Call("HandshakeService.Ping", PingRequest{}, &ping); err != nil {
		t.Fatalf("HandshakeService.Ping: %v", err)
	}
	if ping.Version != "1.0" {
		t.Errorf("Ping version = %q, want \"1.0\" (backward compatible)", ping.Version)
	}

	// The same client can reach Handshake2 on the same service.
	var h2 handshake.Handshake2Reply
	if err := client.Call("HandshakeService.Handshake2", handshake.Handshake2Request{PluginName: "hello"}, &h2); err != nil {
		t.Fatalf("HandshakeService.Handshake2 on the same service: %v", err)
	}
	if h2.PluginRPCVersion != "1.1" {
		t.Errorf("Handshake2 version = %q, want \"1.1\"", h2.PluginRPCVersion)
	}
}

// TestHandshake2ContractReplyFieldsOverMTLS pins that the committed-contract
// reply field names (contract.Handshake2Reply) arrive intact over the real
// gob wire, not just at unit level. A host that decodes into its own
// contract.Handshake2Reply must observe the negotiated values under those
// names; gob matches by field name, so this is the assertion that actually
// protects the host wire contract.
func TestHandshake2ContractReplyFieldsOverMTLS(t *testing.T) {
	ca := newTestCA(t)
	ca.setEnv(t, true, true, true)

	addr, cleanup := startPlugin(t)
	defer cleanup()

	client := dialHost(t, ca, addr)

	var reply handshake.Handshake2Reply
	req := handshake.Handshake2Request{
		HostRPCVersion: "1.1",
		PluginName:     "hello",
	}
	if err := client.Call("HandshakeService.Handshake2", req, &reply); err != nil {
		t.Fatalf("HandshakeService.Handshake2: %v", err)
	}

	if reply.AcceptedProtocolVersion != "1.1" {
		t.Errorf("AcceptedProtocolVersion = %q, want \"1.1\"", reply.AcceptedProtocolVersion)
	}
	if reply.ContributionVersion != handshake.ContributionVersion {
		t.Errorf("ContributionVersion = %q, want %q", reply.ContributionVersion, handshake.ContributionVersion)
	}
	if reply.ContributionVersion != "1.1" {
		t.Errorf("ContributionVersion = %q, want \"1.1\"", reply.ContributionVersion)
	}
	if reply.ServerVersion != handshake.ServerVersion {
		t.Errorf("ServerVersion = %q, want %q", reply.ServerVersion, handshake.ServerVersion)
	}
	if reply.ServerVersion == "" {
		t.Error("ServerVersion must not be empty over the wire")
	}
	// The plugin-named fields must agree with the contract-named ones.
	if reply.AcceptedProtocolVersion != reply.PluginRPCVersion {
		t.Errorf("AcceptedProtocolVersion=%q != PluginRPCVersion=%q",
			reply.AcceptedProtocolVersion, reply.PluginRPCVersion)
	}
}
