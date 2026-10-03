// EU AI Act transparency notice (Art. 50, Regulation (EU) 2024/1689): this
// source file was authored with AI assistance (backend-developer agent) as
// part of the hello 1.0.0 reference-plugin phase A work and is subject to the
// repository's standard human code review before release.
package handshake

import (
	"errors"
	"reflect"
	"testing"
)

// TestHandshake2CorrectNameAgreesOn11 checks the happy path: the host names the
// right plugin and the plugin answers with its newest RPC version and its
// features.
func TestHandshake2CorrectNameAgreesOn11(t *testing.T) {
	svc := NewService()
	var reply Handshake2Reply
	err := svc.Handshake2(&Handshake2Request{PluginName: PluginName}, &reply)
	if err != nil {
		t.Fatalf("Handshake2 returned error: %v", err)
	}
	if reply.PluginRPCVersion != PluginRPCVersion11 {
		t.Errorf("PluginRPCVersion = %q, want %q", reply.PluginRPCVersion, PluginRPCVersion11)
	}
	if reply.PluginRPCVersion != "1.1" {
		t.Errorf("PluginRPCVersion must literally be \"1.1\", got %q", reply.PluginRPCVersion)
	}
	if reply.Name != PluginName {
		t.Errorf("Name = %q, want %q", reply.Name, PluginName)
	}
	if reply.ErrorCode != "" {
		t.Errorf("ErrorCode = %q, want empty on success", reply.ErrorCode)
	}
	if !reflect.DeepEqual(reply.Features, Features) {
		t.Errorf("Features = %v, want %v", reply.Features, Features)
	}
	for _, want := range []string{FeatureRoutes, FeatureJobs, FeatureModels, FeatureNav} {
		if !contains(reply.Features, want) {
			t.Errorf("Features missing %q (got %v)", want, reply.Features)
		}
	}
}

// TestHandshake2WrongPluginNameIsIdentityMismatch verifies that a wrong name
// fails both channels: the returned error (errors.Is ErrIdentityMismatch) and
// the reply's ErrorCode field.
func TestHandshake2WrongPluginNameIsIdentityMismatch(t *testing.T) {
	svc := NewService()
	for _, name := range []string{"", "not-hello", "Hello", "hello "} {
		var reply Handshake2Reply
		err := svc.Handshake2(&Handshake2Request{PluginName: name}, &reply)
		if err == nil {
			t.Errorf("PluginName %q: expected error, got nil", name)
		}
		if !errors.Is(err, ErrIdentityMismatch) {
			t.Errorf("PluginName %q: error = %v, want ErrIdentityMismatch", name, err)
		}
		if reply.ErrorCode != CodeIdentityMismatch {
			t.Errorf("PluginName %q: ErrorCode = %q, want %q", name, reply.ErrorCode, CodeIdentityMismatch)
		}
		if reply.ErrorCode != "E_IDENTITY_MISMATCH" {
			t.Errorf("PluginName %q: ErrorCode must literally be \"E_IDENTITY_MISMATCH\", got %q", name, reply.ErrorCode)
		}
	}
}

// TestHandshake2InstanceIDMismatch pins the optional instance check: an empty
// expectation accepts anything, a pinned one rejects a different id.
func TestHandshake2InstanceIDMismatch(t *testing.T) {
	defer SetExpectedInstanceID("")

	svc := NewService()

	// No expectation: any instance id (including none) is accepted.
	SetExpectedInstanceID("")
	for _, id := range []string{"", "anything"} {
		var reply Handshake2Reply
		if err := svc.Handshake2(&Handshake2Request{PluginName: PluginName, InstanceID: id}, &reply); err != nil {
			t.Errorf("InstanceID %q with no expectation: unexpected error %v", id, err)
		}
	}

	// Pinned expectation: only the exact id passes.
	SetExpectedInstanceID("inst-42")
	var okReply Handshake2Reply
	if err := svc.Handshake2(&Handshake2Request{PluginName: PluginName, InstanceID: "inst-42"}, &okReply); err != nil {
		t.Errorf("matching InstanceID: unexpected error %v", err)
	}

	var badReply Handshake2Reply
	err := svc.Handshake2(&Handshake2Request{PluginName: PluginName, InstanceID: "inst-7"}, &badReply)
	if !errors.Is(err, ErrIdentityMismatch) {
		t.Errorf("mismatched InstanceID: error = %v, want ErrIdentityMismatch", err)
	}
	if badReply.ErrorCode != CodeIdentityMismatch {
		t.Errorf("mismatched InstanceID: ErrorCode = %q, want %q", badReply.ErrorCode, CodeIdentityMismatch)
	}
}

// TestHandshake2IgnoresUnknownHostFeatures verifies that unrecognised host
// features are not an error: the host may be newer than the plugin.
func TestHandshake2IgnoresUnknownHostFeatures(t *testing.T) {
	svc := NewService()
	var reply Handshake2Reply
	req := &Handshake2Request{
		PluginName:   PluginName,
		HostFeatures: []string{"routes", "future-feature", "another-unknown"},
	}
	if err := svc.Handshake2(req, &reply); err != nil {
		t.Fatalf("unknown host features must not fail: %v", err)
	}
	if reply.PluginRPCVersion != PluginRPCVersion11 {
		t.Errorf("PluginRPCVersion = %q, want 1.1", reply.PluginRPCVersion)
	}
}

// TestHandshake2UnknownHostVersionStillAnswers11 checks forward compatibility:
// whatever RPC version the host announces, the plugin still reports its own
// newest one and does not fail.
func TestHandshake2UnknownHostVersionStillAnswers11(t *testing.T) {
	svc := NewService()
	for _, hostVer := range []string{"", "1.0", "1.1", "2.0", "garbage"} {
		var reply Handshake2Reply
		req := &Handshake2Request{PluginName: PluginName, HostRPCVersion: hostVer}
		if err := svc.Handshake2(req, &reply); err != nil {
			t.Errorf("host version %q: unexpected error %v", hostVer, err)
		}
		if reply.PluginRPCVersion != PluginRPCVersion11 {
			t.Errorf("host version %q: PluginRPCVersion = %q, want 1.1", hostVer, reply.PluginRPCVersion)
		}
		if req.ResolveVersion() != PluginRPCVersion11 {
			t.Errorf("host version %q: ResolveVersion = %q, want 1.1", hostVer, req.ResolveVersion())
		}
	}
}

// TestHandshake2AcceptsAliasRequestFields ensures the committed-contract field
// names (ProtocolVersion/Features) are honoured when the Host-prefixed names
// are absent, so a host using the older names still negotiates.
func TestHandshake2AcceptsAliasRequestFields(t *testing.T) {
	req := &Handshake2Request{ProtocolVersion: "1.0"}
	if got := req.ResolveVersion(); got != PluginRPCVersion11 {
		t.Errorf("ResolveVersion = %q, want 1.1", got)
	}
	if got := req.ResolveHostFeatures(); !reflect.DeepEqual(got, []string(nil)) {
		t.Errorf("ResolveHostFeatures with no features = %v, want nil", got)
	}

	alias := &Handshake2Request{Features: []string{"routes"}}
	if got := alias.ResolveHostFeatures(); !reflect.DeepEqual(got, []string{"routes"}) {
		t.Errorf("ResolveHostFeatures alias = %v, want [routes]", got)
	}

	// HostFeatures wins when both are set.
	both := &Handshake2Request{HostFeatures: []string{"jobs"}, Features: []string{"routes"}}
	if got := both.ResolveHostFeatures(); !reflect.DeepEqual(got, []string{"jobs"}) {
		t.Errorf("ResolveHostFeatures preference = %v, want [jobs]", got)
	}
}

// TestHandshake2NilRequestFailsClosed ensures a nil argument is treated as
// missing identity, never as success.
func TestHandshake2NilRequestFailsClosed(t *testing.T) {
	svc := NewService()
	var reply Handshake2Reply
	err := svc.Handshake2(nil, &reply)
	if !errors.Is(err, ErrIdentityMismatch) {
		t.Errorf("nil request: error = %v, want ErrIdentityMismatch", err)
	}
}

// TestHandshake2ReplyCarriesContractFieldNames verifies the reply is populated
// under BOTH naming schemes: the plugin names and the committed host contract
// names (contract.Handshake2Reply). A host that decodes by either name must see
// the same negotiated result.
func TestHandshake2ReplyCarriesContractFieldNames(t *testing.T) {
	svc := NewService()
	var reply Handshake2Reply
	if err := svc.Handshake2(&Handshake2Request{PluginName: PluginName}, &reply); err != nil {
		t.Fatalf("Handshake2 returned error: %v", err)
	}

	// Plugin names.
	if reply.PluginRPCVersion != PluginRPCVersion11 {
		t.Errorf("PluginRPCVersion = %q, want %q", reply.PluginRPCVersion, PluginRPCVersion11)
	}
	// Committed-contract names must mirror the plugin names exactly.
	if reply.AcceptedProtocolVersion != reply.PluginRPCVersion {
		t.Errorf("AcceptedProtocolVersion = %q, want %q (== PluginRPCVersion)",
			reply.AcceptedProtocolVersion, reply.PluginRPCVersion)
	}
	if reply.AcceptedProtocolVersion != "1.1" {
		t.Errorf("AcceptedProtocolVersion must literally be \"1.1\", got %q", reply.AcceptedProtocolVersion)
	}
	if reply.ContributionVersion != ContributionVersion {
		t.Errorf("ContributionVersion = %q, want %q", reply.ContributionVersion, ContributionVersion)
	}
	if reply.ContributionVersion != "1.1" {
		t.Errorf("ContributionVersion must literally be \"1.1\", got %q", reply.ContributionVersion)
	}
	if reply.ServerVersion != ServerVersion {
		t.Errorf("ServerVersion = %q, want %q", reply.ServerVersion, ServerVersion)
	}
	if reply.ServerVersion == "" {
		t.Error("ServerVersion must not be empty")
	}
	if !reflect.DeepEqual(reply.Features, Features) {
		t.Errorf("Features = %v, want %v", reply.Features, Features)
	}
}

// TestHandshake2MismatchReplyCarriesContractFieldNames pins that the identity
// failure path also fills the contract-named fields, so a peer logging the
// reply sees which plugin it actually reached.
func TestHandshake2MismatchReplyCarriesContractFieldNames(t *testing.T) {
	svc := NewService()
	var reply Handshake2Reply
	err := svc.Handshake2(&Handshake2Request{PluginName: "not-hello"}, &reply)
	if !errors.Is(err, ErrIdentityMismatch) {
		t.Fatalf("error = %v, want ErrIdentityMismatch", err)
	}
	if reply.ErrorCode != CodeIdentityMismatch {
		t.Errorf("ErrorCode = %q, want %q", reply.ErrorCode, CodeIdentityMismatch)
	}
	if reply.AcceptedProtocolVersion != PluginRPCVersion11 {
		t.Errorf("AcceptedProtocolVersion = %q, want %q", reply.AcceptedProtocolVersion, PluginRPCVersion11)
	}
	if reply.ContributionVersion != ContributionVersion {
		t.Errorf("ContributionVersion = %q, want %q", reply.ContributionVersion, ContributionVersion)
	}
	if reply.ServerVersion != ServerVersion {
		t.Errorf("ServerVersion = %q, want %q", reply.ServerVersion, ServerVersion)
	}
}

// contains reports whether s holds want.
func contains(s []string, want string) bool {
	for _, v := range s {
		if v == want {
			return true
		}
	}
	return false
}
