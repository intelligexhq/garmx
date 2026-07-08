package aggregator

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Delimiter separates a registered server name from an upstream tool/prompt
// name in an exposed, client-facing name (AWS AgentCore convention). The triple
// underscore is safe because registered server names forbid underscores
// (serverNameRe), so the FIRST "___" in an exposed name is unambiguously the
// boundary — even when the upstream's own name itself contains "___".
const Delimiter = "___"

// maxServerNameLen bounds a registered server name so the prefix cannot consume
// an unreasonable share of a client's tool-name budget (see LengthBudget).
const maxServerNameLen = 32

// LengthBudget is the exposed-name length above which GarmX warns at
// registration. Common clients cap tool names near 64 chars; 60 leaves
// headroom. GarmX never truncates — that would break the reversible Split — it
// warns and surfaces the offending tools in the UI. Downstream clients wrap the
// exposed name again (Claude Code presents it as mcp__<garmx>__<exposed>), so
// short server names matter for staying within the real client-side limit.
const LengthBudget = 60

// serverNameRe constrains registered server names to lowercase alphanumerics
// and hyphens, starting alphanumeric, with no underscores. Forbidding
// underscores is exactly what makes the first-"___" Split unambiguous.
var serverNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// ErrInvalidServerName is returned when a server name violates serverNameRe or
// the length bound.
var ErrInvalidServerName = errors.New("invalid server name")

// ValidateServerName enforces the naming contract that guarantees an
// unambiguous prefix Split. It rejects the empty name, names longer than
// maxServerNameLen, and names not matching serverNameRe (which excludes
// underscores). Registration calls this before accepting a server.
func ValidateServerName(server string) error {
	if server == "" || len(server) > maxServerNameLen || !serverNameRe.MatchString(server) {
		return fmt.Errorf("%w: %q", ErrInvalidServerName, server)
	}
	return nil
}

// Prefix builds the exposed, client-facing name for an upstream tool or prompt
// as "<server>___<name>". Callers are expected to have validated server via
// ValidateServerName at registration time.
func Prefix(server, name string) string {
	return server + Delimiter + name
}

// Split reverses Prefix: it splits an exposed name on the FIRST "___" into the
// registered server name and the upstream's original name. ok is false when
// there is no delimiter, when either side is empty, or when the server side is
// not a valid server name. Splitting on the first delimiter (not the last) lets
// an upstream's original name itself contain "___" and still round-trip.
func Split(exposed string) (server, name string, ok bool) {
	i := strings.Index(exposed, Delimiter)
	if i <= 0 {
		return "", "", false
	}
	server = exposed[:i]
	name = exposed[i+len(Delimiter):]
	if name == "" || ValidateServerName(server) != nil {
		return "", "", false
	}
	return server, name, true
}

// ExceedsLengthBudget reports whether an exposed name is long enough to risk
// truncation by a downstream client. It is advisory: registration proceeds, but
// GarmX warns and the UI flags the tool. See LengthBudget.
func ExceedsLengthBudget(exposed string) bool {
	return len(exposed) > LengthBudget
}
