package config

import (
	"strings"
	"testing"
)

// boolp is a helper for the optional Enabled field.
func boolp(b bool) *bool { return &b }

// TestResolveAuditDefaults confirms an absent audit block yields the built-in
// defaults (enabled, request-response, calls, XDG path, 16 KiB cap).
func TestResolveAuditDefaults(t *testing.T) {
	r, err := ResolveAudit(nil, AuditOverride{})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Enabled {
		t.Error("default should be enabled")
	}
	if r.Payload != PayloadRequestResponse || r.Scope != ScopeCalls {
		t.Errorf("defaults payload/scope = %q/%q", r.Payload, r.Scope)
	}
	if r.MaxPayloadBytes != defaultMaxPayloadBytes {
		t.Errorf("default max bytes = %d, want %d", r.MaxPayloadBytes, defaultMaxPayloadBytes)
	}
	if !strings.HasSuffix(r.DBPath, "garmx/audit.db") {
		t.Errorf("default path = %q, want .../garmx/audit.db", r.DBPath)
	}
}

// TestResolveAuditPrecedence confirms flags/env override the file, which
// overrides the defaults.
func TestResolveAuditPrecedence(t *testing.T) {
	file := &AuditConfig{
		Enabled:         boolp(true),
		DBPath:          "/from/file.db",
		Payload:         PayloadRequest,
		Scope:           ScopeAll,
		MaxPayloadBytes: 4096,
	}
	// File values win over defaults.
	r, err := ResolveAudit(file, AuditOverride{})
	if err != nil {
		t.Fatal(err)
	}
	if r.DBPath != "/from/file.db" || r.Payload != PayloadRequest || r.Scope != ScopeAll || r.MaxPayloadBytes != 4096 {
		t.Errorf("file resolution = %+v", r)
	}
	// Overrides win over the file.
	r, err = ResolveAudit(file, AuditOverride{DBPath: "/from/flag.db", Payload: PayloadMetadata, Scope: ScopeCalls, Disable: true})
	if err != nil {
		t.Fatal(err)
	}
	if r.DBPath != "/from/flag.db" || r.Payload != PayloadMetadata || r.Scope != ScopeCalls || r.Enabled {
		t.Errorf("override resolution = %+v", r)
	}
}

// TestResolveAuditRejectsBadEnum confirms an invalid payload/scope errors.
func TestResolveAuditRejectsBadEnum(t *testing.T) {
	if _, err := ResolveAudit(&AuditConfig{Payload: "everything"}, AuditOverride{}); err == nil {
		t.Error("bad payload should error")
	}
	if _, err := ResolveAudit(nil, AuditOverride{Scope: "some"}); err == nil {
		t.Error("bad scope override should error")
	}
}

// TestLoadRejectsBadAuditEnum confirms a bad audit enum in the file fails at
// load, consistent with the rest of config validation.
func TestLoadRejectsBadAuditEnum(t *testing.T) {
	content := `{"servers":[{"name":"x","command":"/a"}],"audit":{"scope":"sometimes"}}`
	if _, err := Load(writeConfig(t, content)); err == nil {
		t.Error("bad audit.scope should fail Load")
	}
}
