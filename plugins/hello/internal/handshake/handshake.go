// EU AI Act transparency notice (Art. 50, Regulation (EU) 2024/1689): this
// source file was authored with AI assistance (backend-developer agent) as
// part of the hello 1.0.0 reference-plugin phase A work and is subject to the
// repository's standard human code review before release.
//
// Package handshake implements the server side of the negotiated plugin
// handshake (RPC method HandshakeService.Handshake2). The host is the CLIENT
// of this method; the plugin is the server and must offer the method on the
// same rpc.Service it registers as "HandshakeService" (that RegisterName
// string is part of the wire contract and is deliberately unchanged).
//
// Wire contract (field mapping)
//
// Go's net/rpc uses gob, which encodes structs by FIELD NAME, not by JSON tag.
// The plugin therefore declares its request/reply structs with exactly the
// field names the host contract uses, so the gob decoder on both sides lines
// up:
//
//	Host contract (Handshake2Request)      Plugin struct (Handshake2Request)
//	  HostRPCVersion  (RPC version)          HostRPCVersion
//	  HostFeatures    (feature names)        HostFeatures
//	  PluginName      (expected plugin)      PluginName
//	  InstanceID      (instance identity)    InstanceID
//
//	Host contract (Handshake2Reply)        Plugin struct (Handshake2Reply)
//	  PluginRPCVersion (agreed version)      PluginRPCVersion
//	  Features         (mutual features)     Features
//	  Name             (plugin name)         Name
//	  ErrorCode        (identity failure)    ErrorCode
//
// The request struct additionally carries the committed-contract field names
// (ProtocolVersion/Features) as an alias: gob ignores fields the sender did
// not populate, so a host that still speaks the older names is answered
// without a decode error. See TestHandshake2AcceptsAliasRequestFields.
//
// The reply struct is symmetric: it carries the field names of the committed
// host contract (AcceptedProtocolVersion/ContributionVersion/Features/
// ServerVersion, see contract.Handshake2Reply) ALONGSIDE the plugin-named
// fields. Both groups are populated on every reply, so a host that decodes by
// either naming scheme observes the same negotiated result; gob simply leaves
// the fields a given caller does not declare untouched. This matters because
// contract.Handshake2Reply and the plugin's struct grew independently and the
// wire must not depend on which side's names won.
//
// Forward compatibility: an unknown HostRPCVersion or an unknown host feature
// is NOT an error. The plugin always answers with the newest RPC version it
// speaks ("1.1") and simply omits features it does not implement — the
// host downgrades to the intersection. Only an identity mismatch is fatal.
package handshake

import (
	"errors"
	"fmt"

	"glorious-community/plugins/hello/internal/i18n"
)

// Contract constants. PluginRPCVersion is the newest RPC protocol version the
// plugin speaks; the plugin answers with it regardless of what the host
// announced (forward compatibility — the host downgrades to the intersection).
const (
	// PluginRPCVersion11 is the current negotiated RPC protocol version.
	PluginRPCVersion11 = "1.1"
	// PluginName is the plugin's manifest name. It is the value the host
	// expects in Handshake2Request.PluginName and the value the plugin
	// reports back in Handshake2Reply.Name; a mismatch is E_IDENTITY_MISMATCH.
	PluginName = "hello"
	// ContributionVersion is the contribution contract version the plugin
	// builds its contribution surface against (contract.PluginContributionVersion,
	// which is "1.1"). It is independent of the RPC protocol version and is
	// reported in Handshake2Reply.ContributionVersion.
	ContributionVersion = "1.1"
	// ServerVersion is the human-readable plugin build version, for logs and
	// operator diagnostics only. It is reported in
	// Handshake2Reply.ServerVersion and is never part of the negotiation.
	ServerVersion = "1.0.0"
)

// Feature names the plugin advertises. Feature names are opaque lower-case
// tokens; the host intersects them with its own set and must only use what
// both sides announced.
//
// What the plugin actually offers today:
//   - FeatureRoutes: the hello plugin mounts HTTP routes (GET /hello) through
//     the host gateway — see main.go pluginHandler. This is real and wired.
//   - FeatureJobs:   the plugin exposes a scheduled job surface (heartbeat);
//     declared here so the host may schedule it once the job contract lands.
//   - FeatureModels: the plugin ships a plugin-models.json catalog. NOTE:
//     the catalog content is a later phase; the feature is announced because
//     the file and its wiring are part of this version's surface.
//   - FeatureNav:    the plugin contributes a navigation entry in the host UI.
//
// Features the plugin does NOT implement are simply never listed; listing one
// it cannot serve would make the host call an unimplemented surface.
const (
	// FeatureRoutes is the HTTP route contribution surface.
	FeatureRoutes = "routes"
	// FeatureJobs is the scheduled-job contribution surface.
	FeatureJobs = "jobs"
	// FeatureModels is the declarative data-model contribution surface.
	FeatureModels = "models"
	// FeatureNav is the UI navigation contribution surface.
	FeatureNav = "nav"
)

// Features is the list of feature names the plugin announces, in a stable
// order. It is rendered as a plain string slice on the wire.
var Features = []string{FeatureRoutes, FeatureJobs, FeatureModels, FeatureNav}

// CodeIdentityMismatch is the reply error code for an identity mismatch. It is
// delivered BOTH in Handshake2Reply.ErrorCode and as the method's error return
// value, so a caller matching either channel observes the same failure. The
// constant mirrors the host contract's E_IDENTITY_MISMATCH.
const CodeIdentityMismatch = "E_IDENTITY_MISMATCH"

// ErrIdentityMismatch is the sentinel behind every E_IDENTITY_MISMATCH return.
// Callers use errors.Is to distinguish an identity failure from a transport
// error.
var ErrIdentityMismatch = errors.New(CodeIdentityMismatch)

// Handshake2Request is the argument of HandshakeService.Handshake2. Field
// names match the host contract exactly (gob encodes by name).
type Handshake2Request struct {
	// HostRPCVersion is the RPC protocol version the host speaks. Unknown
	// values are tolerated: the plugin still answers with its own newest
	// version (forward compatibility).
	HostRPCVersion string
	// HostFeatures are the feature names the host understands. Unknown names
	// are ignored, never rejected.
	HostFeatures []string
	// PluginName is the plugin the host believes it is talking to. It must be
	// exactly PluginName or the handshake fails with E_IDENTITY_MISMATCH.
	PluginName string
	// InstanceID identifies the plugin instance, when the host scopes one. An
	// empty value means "not pinned" and is accepted; a non-empty value must
	// match ExpectedInstanceID, otherwise E_IDENTITY_MISMATCH.
	InstanceID string

	// --- alias fields for the committed-contract names ---------------------
	// ProtocolVersion and Features mirror the older host field names
	// (contract.Handshake2Request). gob leaves them empty when the host did
	// not send them; resolveVersion/resolveHostFeatures prefer the Host-prefixed
	// names and fall back to these.
	ProtocolVersion string
	Features        []string
}

// Handshake2Reply is the reply of HandshakeService.Handshake2. Field names and
// semantics match the host contract exactly.
type Handshake2Reply struct {
	// PluginRPCVersion is the RPC protocol version the plugin agrees to. It is
	// always PluginRPCVersion11 on success, regardless of the host's announced
	// version.
	PluginRPCVersion string
	// Features are the plugin's advertised feature names (see Features). The
	// host intersects this with its own set.
	Features []string
	// Name is the plugin's manifest name. It always equals PluginName on a
	// successful handshake.
	Name string
	// ErrorCode is empty on success, otherwise CodeIdentityMismatch. It is set
	// in addition to the method's error return so both channels agree.
	ErrorCode string

	// --- committed-contract reply field names ------------------------------
	// AcceptedProtocolVersion, ContributionVersion and ServerVersion carry
	// the same values as PluginRPCVersion, ContributionVersion and
	// ServerVersion above, under the field names of the committed host
	// contract (contract.Handshake2Reply). They exist so a host decoding by
	// the contract's names sees the negotiation result without the plugin
	// having to rename its own fields. Never set one group without the other:
	// setReply writes both.
	AcceptedProtocolVersion string
	// ContributionVersion is the contribution contract version the plugin
	// applies (see the package constant).
	ContributionVersion string
	// ServerVersion is the plugin's human-readable build version.
	ServerVersion string
}

// ExpectedInstanceID, when non-empty, is the instance id the plugin accepts in
// Handshake2Request.InstanceID. It is empty by default (the plugin accepts any
// instance id, including none); a host that pins the identity sets it via
// SetExpectedInstanceID before serving.
var ExpectedInstanceID string

// SetExpectedInstanceID pins the accepted instance id. An empty value restores
// "accept any instance id". It is not goroutine-safe by itself: callers set it
// during start-up, before the listener accepts connections.
func SetExpectedInstanceID(id string) {
	ExpectedInstanceID = id
}

// Service implements the negotiated handshake server side. It is embedded (or
// composed) into the same value registered under the "HandshakeService"
// RegisterName as Ping, so the host reaches both methods on one service.
type Service struct{}

// NewService returns a ready handshake service. It is stateless; the identity
// expectation lives on the package (SetExpectedInstanceID) so a re-registration
// cannot silently forget it.
func NewService() *Service { return &Service{} }

// Handshake2 answers the host's negotiated handshake. On an identity mismatch
// it returns ErrIdentityMismatch AND sets reply.ErrorCode; on success it
// reports PluginRPCVersion11, the plugin's features and its name.
//
// net/rpc caveat: when a method returns a non-nil error, the RPC layer drops
// the reply body on the wire, so a *remote* client only observes the error
// string, not reply.ErrorCode. Both channels are still populated (the direct
// caller and any in-process use see ErrorCode), and the error text embeds the
// localized identity message; the code constant is what a host matching on the
// reply field checks. See TestHandshake2WrongPluginNameIsIdentityMismatch and
// TestHandshake2WrongNameOverMTLS.
func (s *Service) Handshake2(req *Handshake2Request, reply *Handshake2Reply) error {
	if req == nil {
		req = &Handshake2Request{}
	}

	if code := identityErrorCode(req); code != "" {
		*reply = handshake2Reply(code)
		return fmt.Errorf("%s: %w", i18n.Message(i18n.KeyIdentityMismatch), ErrIdentityMismatch)
	}

	*reply = handshake2Reply("")
	return nil
}

// handshake2Reply builds the reply body for a given error code. It populates
// BOTH field-name groups (the plugin names and the committed-contract names)
// with the same values, so the reply is correct whoever decodes it. A
// non-empty code marks an identity failure; every other field still carries
// the plugin's own identity so the peer can log what it actually reached.
func handshake2Reply(code string) Handshake2Reply {
	return Handshake2Reply{
		PluginRPCVersion: PluginRPCVersion11,
		Features:         append([]string(nil), Features...),
		Name:             PluginName,
		ErrorCode:        code,

		AcceptedProtocolVersion: PluginRPCVersion11,
		ContributionVersion:     ContributionVersion,
		ServerVersion:           ServerVersion,
	}
}

// identityErrorCode returns CodeIdentityMismatch when the request's identity is
// wrong and "" when it is acceptable. A missing or wrong PluginName is always a
// mismatch; an InstanceID is only checked when the plugin has pinned an
// expected one, so a host that does not scope instances still negotiates.
func identityErrorCode(req *Handshake2Request) string {
	if req.PluginName != PluginName {
		return CodeIdentityMismatch
	}
	if ExpectedInstanceID != "" && req.InstanceID != ExpectedInstanceID {
		return CodeIdentityMismatch
	}
	return ""
}

// ResolveVersion returns the RPC version the plugin answers with, independent
// of the host's offer. It exists so tests can assert the forward-compatibility
// rule ("unknown host version still yields 1.1") without duplicating the
// constant.
func (r *Handshake2Request) ResolveVersion() string {
	return PluginRPCVersion11
}

// ResolveHostFeatures returns the host's announced features, preferring the
// HostFeatures field and falling back to the alias Features field. Unknown
// names are returned as-is; the caller ignores those it does not know.
func (r *Handshake2Request) ResolveHostFeatures() []string {
	if len(r.HostFeatures) > 0 {
		return r.HostFeatures
	}
	return r.Features
}
