package aggregator

import "path/filepath"

// Profile is a static, curation-first scoping of the aggregate exposed to one
// client session: a subset of servers plus tool allow/deny patterns. The zero
// Profile exposes everything (the default when a client connects with no
// --profile). Patterns match against the exposed (prefixed) tool name.
type Profile struct {
	// Servers is the allowed server-name subset; empty means all servers.
	Servers []string
	// ToolAllow lists exposed-name globs; empty means all tools are allowed
	// (subject to ToolDeny). A non-empty allow list is a whitelist.
	ToolAllow []string
	// ToolDeny lists exposed-name globs that are always hidden. Deny wins.
	ToolDeny []string
}

// AllowsServer reports whether server is in scope for this profile.
func (p Profile) AllowsServer(server string) bool {
	if len(p.Servers) == 0 {
		return true
	}
	for _, s := range p.Servers {
		if s == server {
			return true
		}
	}
	return false
}

// AllowsTool reports whether an exposed tool name is visible under this profile.
// Deny is evaluated first and wins; then, if an allow list exists, the name must
// match it. Names have no path separators, so filepath.Match's `*` spans the
// whole name (including the "___" delimiter), which is what patterns like
// "github___*" and "*___delete_*" rely on.
func (p Profile) AllowsTool(exposed string) bool {
	for _, pat := range p.ToolDeny {
		if globMatch(pat, exposed) {
			return false
		}
	}
	if len(p.ToolAllow) == 0 {
		return true
	}
	for _, pat := range p.ToolAllow {
		if globMatch(pat, exposed) {
			return true
		}
	}
	return false
}

// globMatch reports whether name matches the glob pattern, treating a malformed
// pattern as a non-match rather than an error.
func globMatch(pattern, name string) bool {
	ok, err := filepath.Match(pattern, name)
	return err == nil && ok
}
