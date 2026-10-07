// EU AI Act, Art. 50 (Regulation (EU) 2024/1689): this test file was authored
// with AI assistance and is subject to human review before release.
package main

import (
	"reflect"
	"testing"
)

// TestDescribeContributionsDeclaresRoute pins the route declaration: the
// contribution list must be non-empty and must name exactly the GET /hello
// route the WebService serves. An empty list is what leaves the host's HTTP
// mount dark, so this is the regression guard for the E2E blocker.
func TestDescribeContributionsDeclaresRoute(t *testing.T) {
	var resp DescribeResp
	if err := (ContributionService{}).DescribeContributions(DescribeContributionsRequest{}, &resp); err != nil {
		t.Fatalf("DescribeContributions: %v", err)
	}
	if len(resp.Routes) == 0 {
		t.Fatal("declared route list is empty; the host would mount nothing")
	}
	got := resp.Routes[0]
	if got.Method != "GET" && got.Method != "" {
		t.Errorf("route method = %q, want GET or empty (=GET)", got.Method)
	}
	if got.Path != routePath {
		t.Errorf("route path = %q, want %q", got.Path, routePath)
	}
	if got.Object != routeObject || got.Action != routeAction {
		t.Errorf("route RBAC binding = %q.%q, want %q.%q", got.Object, got.Action, routeObject, routeAction)
	}
	if resp.ErrorCode != "" {
		t.Errorf("ErrorCode = %q, want empty on success", resp.ErrorCode)
	}
}

// TestServeHTTPRouteAnswers verifies the forwarded-request handler returns the
// expected status, content type and body for the declared route, and a clean
// 404 for an undeclared path.
func TestServeHTTPRouteAnswers(t *testing.T) {
	var resp HTTPResp
	if err := (WebService{}).ServeHTTP(HTTPReq{Method: "GET", Path: routePath}, &resp); err != nil {
		t.Fatalf("ServeHTTP: %v", err)
	}
	if resp.Status != 200 {
		t.Errorf("status = %d, want 200", resp.Status)
	}
	if string(resp.Body) != routeBody {
		t.Errorf("body = %q, want %q", resp.Body, routeBody)
	}
	if ct := resp.Header["Content-Type"]; len(ct) == 0 || ct[0] != "text/plain; charset=utf-8" {
		t.Errorf("Content-Type = %v, want text/plain; charset=utf-8", ct)
	}

	var miss HTTPResp
	if err := (WebService{}).ServeHTTP(HTTPReq{Method: "GET", Path: "/nope"}, &miss); err != nil {
		t.Fatalf("ServeHTTP (undeclared): %v", err)
	}
	if miss.Status != 404 {
		t.Errorf("undeclared path status = %d, want 404", miss.Status)
	}
}

// TestPingAnnouncesV11 pins the gate the host checks first: the mandatory
// HandshakeService must report "1.1". A "1.0" answer would stop the host before
// Handshake2 and nothing would mount.
func TestPingAnnouncesV11(t *testing.T) {
	var resp PingResponse
	if err := (HandshakeService{}).Ping(PingRequest{}, &resp); err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if resp.Version != "1.1" {
		t.Errorf("Ping version = %q, want 1.1", resp.Version)
	}
}

// TestHandshake2ReportsV11Identity pins the negotiated handshake reply: the
// host rejects the reply fail-closed unless PluginRPCVersion is "1.1" and the
// Name/InstanceID match the addressed plugin.
func TestHandshake2ReportsV11Identity(t *testing.T) {
	svc := ContributionService{manifestName: manifestName}
	var resp Handshake2Resp
	req := Handshake2Req{HostRPCVersion: "1.1", PluginName: manifestName, InstanceID: instanceIDV11}
	if err := svc.Handshake2(req, &resp); err != nil {
		t.Fatalf("Handshake2: %v", err)
	}
	if resp.PluginRPCVersion != "1.1" {
		t.Errorf("PluginRPCVersion = %q, want 1.1", resp.PluginRPCVersion)
	}
	if resp.Name != manifestName || resp.InstanceID != instanceIDV11 {
		t.Errorf("identity = %q/%q, want %q/%q", resp.Name, resp.InstanceID, manifestName, instanceIDV11)
	}
	if len(resp.Features) == 0 || resp.Features[0] != featureRoutes {
		t.Errorf("Features = %v, want it to contain %q", resp.Features, featureRoutes)
	}
	if resp.ContributionVersion != "1.1" {
		t.Errorf("ContributionVersion = %q, want 1.1", resp.ContributionVersion)
	}
}

// TestHandshake2RejectsForeignPlugin proves the identity check is fail-closed:
// a handshake addressed to a different plugin must error, never be answered.
func TestHandshake2RejectsForeignPlugin(t *testing.T) {
	svc := ContributionService{manifestName: manifestName}
	var resp Handshake2Resp
	if err := svc.Handshake2(Handshake2Req{PluginName: "someone-else"}, &resp); err == nil {
		t.Fatal("Handshake2 accepted a request addressed to a different plugin")
	}
}

// TestRPCServicesExposeExpectedMethodNames pins the net/rpc surface: each
// registered service must expose exactly the method the host builds its call
// from. A rename here would surface as "rpc: can't find service" on the host.
func TestRPCServicesExposeExpectedMethodNames(t *testing.T) {
	cases := []struct {
		recv   any
		method string
	}{
		{HandshakeService{}, "HandshakeService.Ping"},
		{ContributionService{}, "ContributionService.Handshake2"},
		{ContributionService{}, "ContributionService.DescribeContributions"},
		{WebService{}, "WebService.ServeHTTP"},
	}
	for _, tc := range cases {
		if !hasRPCMethod(tc.recv, tc.method) {
			t.Errorf("method %s is not exposed by %T", tc.method, tc.recv)
		}
	}
}

// hasRPCMethod reports whether recv exposes the given "Service.Method" name
// with the signature net/rpc requires (two args, last a pointer, one error).
func hasRPCMethod(recv any, name string) bool {
	typ := reflect.TypeOf(recv)
	for i := 0; i < typ.NumMethod(); i++ {
		m := typ.Method(i)
		if typ.Name()+"."+m.Name != name {
			continue
		}
		if m.Type.NumIn() != 3 {
			return false
		}
		if m.Type.In(2).Kind() != reflect.Ptr {
			return false
		}
		if m.Type.NumOut() != 1 || !m.Type.Out(0).Implements(reflect.TypeOf((*error)(nil)).Elem()) {
			return false
		}
		return true
	}
	return false
}

// TestWireFieldNamesMatchHostContract pins the gob wire contract by field name.
// The community repo cannot import the host's internal contract package, so a
// field renamed here would only fail at runtime on the host as a silent
// decode mismatch. These lists are transcribed from
// glorious-platform_v2 internal/plugins/contract (handshake_host.go,
// contribution.go, webservice.go) and must stay in sync by hand.
func TestWireFieldNamesMatchHostContract(t *testing.T) {
	want := []struct {
		sample any
		fields []string
	}{
		{PingResponse{}, []string{"Version"}},
		{Handshake2Req{}, []string{"HostRPCVersion", "HostFeatures", "PluginName", "InstanceID"}},
		{Handshake2Resp{}, []string{"PluginRPCVersion", "Features", "Name", "InstanceID", "AcceptedProtocolVersion", "ContributionVersion", "ServerVersion"}},
		{RouteSpec{}, []string{"Method", "Path", "Object", "Action", "Public", "Streaming", "MaxBodyBytes"}},
		{DescribeResp{}, []string{"Routes", "Templates", "Assets", "ErrorCode"}},
		{HTTPReq{}, []string{"Method", "Path", "Query", "Header", "Body", "PluginName", "InstanceID", "Principal", "RequestID", "CSRFOK"}},
		{HTTPResp{}, []string{"Status", "Header", "Body", "StreamNext", "ErrorCode"}},
	}
	for _, tc := range want {
		fields := tc.fields
		typ := reflect.TypeOf(tc.sample)
		if typ.NumField() != len(fields) {
			t.Errorf("%s: %d fields, want %d", typ.Name(), typ.NumField(), len(fields))
			continue
		}
		for i, name := range fields {
			if got := typ.Field(i).Name; got != name {
				t.Errorf("%s field %d = %q, want %q", typ.Name(), i, got, name)
			}
		}
	}
}
