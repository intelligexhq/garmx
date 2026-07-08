// Package mdlint provides a small, dependency-free Markdown normalizer that
// keeps the repository's documentation consistently formatted.
//
// It is deliberately not a full CommonMark linter. It enforces a fixed set of
// structural rules by rewriting a document to a canonical form:
//
//   - blank lines around ATX headings, top-level lists, and fenced code blocks;
//   - a language token on every opening code fence (defaulting to "text");
//   - no trailing whitespace and no consecutive blank lines outside code;
//   - exactly one trailing newline.
//
// Callers compare Normalize's output against the input to check formatting, or
// write it back to fix it. Content inside fenced code blocks is preserved
// verbatim so diagrams and sample output are never altered.
package mdlint

import "strings"

// defaultFenceLang is applied to an opening code fence that omits a language,
// so plain diagrams and command output still satisfy the "fenced code needs a
// language" rule without a human choosing a token.
const defaultFenceLang = "text"

// lineKind classifies a source line for blank-line placement decisions.
type lineKind int

const (
	kindBlank lineKind = iota
	kindHeading
	kindFenceOpen
	kindFenceClose
	kindFenceBody
	kindListItem
	kindListCont // indented continuation line of a list item
	kindOther
)

// Normalize rewrites src into the repository's canonical Markdown form. It is
// idempotent — Normalize(Normalize(x)) equals Normalize(x) — and returns a
// document that is already canonical byte-for-byte unchanged, so callers can
// use equality with the input as the "is this file formatted?" check.
func Normalize(src []byte) []byte {
	text := string(src)
	// Normalize line endings so the result is stable across platforms.
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")

	lines := strings.Split(text, "\n")
	// A trailing newline leaves a final empty element; drop it so the final
	// newline is controlled solely by the join below.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	lines, kinds := classify(lines)
	out, outKinds := insertSeparators(lines, kinds)
	cleaned := collapse(out, outKinds)
	if len(cleaned) == 0 {
		return []byte{}
	}
	return []byte(strings.Join(cleaned, "\n") + "\n")
}

// classify canonicalizes each line (trimming trailing whitespace and adding a
// fence language) and tags it with a lineKind. Lines inside a fenced code block
// are returned verbatim and tagged kindFenceBody so later passes leave them
// untouched.
func classify(lines []string) ([]string, []lineKind) {
	kinds := make([]lineKind, len(lines))
	inFence := false
	inList := false
	for i, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		isFence := strings.HasPrefix(trimmed, "```")
		switch {
		case inFence && !isFence:
			kinds[i] = kindFenceBody
		case isFence && !inFence:
			lines[i] = canonicalFenceOpen(ln)
			kinds[i] = kindFenceOpen
			inFence = true
			inList = false
		case isFence && inFence:
			kinds[i] = kindFenceClose
			inFence = false
		default:
			ln = strings.TrimRight(ln, " \t")
			lines[i] = ln
			trimmed = strings.TrimSpace(ln)
			switch {
			case trimmed == "":
				kinds[i] = kindBlank
			case isHeading(trimmed):
				kinds[i] = kindHeading
				inList = false
			case isListItem(trimmed):
				kinds[i] = kindListItem
				inList = true
			case inList && isIndented(ln):
				kinds[i] = kindListCont
			default:
				kinds[i] = kindOther
				inList = false
			}
		}
	}
	return lines, kinds
}

// insertSeparators rebuilds the line list, inserting a single blank line
// wherever structure requires one. A start-of-file is treated as if preceded
// by a blank so no separator is added before the first line. Duplicate blanks
// introduced here are removed by collapse.
func insertSeparators(lines []string, kinds []lineKind) ([]string, []lineKind) {
	out := make([]string, 0, len(lines))
	outKinds := make([]lineKind, 0, len(lines))
	prev := kindBlank
	for i, ln := range lines {
		k := kinds[i]
		if needBlankBetween(prev, k) && len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
			outKinds = append(outKinds, kindBlank)
		}
		out = append(out, ln)
		outKinds = append(outKinds, k)
		prev = k
	}
	return out, outKinds
}

// collapse removes runs of consecutive blank lines and strips leading and
// trailing blanks. Blank lines inside fenced code (kindFenceBody) are never
// collapsed, preserving intentional spacing in samples.
func collapse(lines []string, kinds []lineKind) []string {
	cleaned := make([]string, 0, len(lines))
	cleanedKinds := make([]lineKind, 0, len(lines))
	for i, ln := range lines {
		k := kinds[i]
		if ln == "" && k == kindBlank &&
			len(cleaned) > 0 && cleaned[len(cleaned)-1] == "" &&
			cleanedKinds[len(cleanedKinds)-1] == kindBlank {
			continue
		}
		cleaned = append(cleaned, ln)
		cleanedKinds = append(cleanedKinds, k)
	}
	for len(cleaned) > 0 && cleaned[0] == "" && cleanedKinds[0] == kindBlank {
		cleaned = cleaned[1:]
		cleanedKinds = cleanedKinds[1:]
	}
	for len(cleaned) > 0 && cleaned[len(cleaned)-1] == "" &&
		cleanedKinds[len(cleanedKinds)-1] == kindBlank {
		cleaned = cleaned[:len(cleaned)-1]
		cleanedKinds = cleanedKinds[:len(cleanedKinds)-1]
	}
	return cleaned
}

// needBlankBetween reports whether a blank line must separate two adjacent
// non-blank lines of the given kinds. It encodes the blanks-around rules for
// headings, fenced code, and list boundaries while never inserting a blank
// inside a fenced block.
func needBlankBetween(prev, cur lineKind) bool {
	if prev == kindBlank || cur == kindBlank {
		return false
	}
	if prev == kindFenceBody || cur == kindFenceBody {
		return false
	}
	switch {
	case cur == kindHeading, prev == kindHeading:
		return true
	case cur == kindFenceOpen, prev == kindFenceClose:
		return true
	case cur == kindListItem && prev != kindListItem && prev != kindListCont:
		return true
	case (prev == kindListItem || prev == kindListCont) &&
		cur != kindListItem && cur != kindListCont:
		return true
	}
	return false
}

// canonicalFenceOpen ensures an opening code fence declares a language,
// defaulting to defaultFenceLang, while preserving any leading indentation.
func canonicalFenceOpen(line string) string {
	indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	rest := strings.TrimLeft(line, " \t")
	info := strings.TrimSpace(strings.TrimLeft(rest, "`"))
	if info == "" {
		info = defaultFenceLang
	}
	return indent + "```" + info
}

// isHeading reports whether a whitespace-trimmed line is an ATX heading
// (one-to-six leading '#' followed by a space).
func isHeading(trimmed string) bool {
	n := 0
	for n < len(trimmed) && trimmed[n] == '#' {
		n++
	}
	return n >= 1 && n <= 6 && n < len(trimmed) && trimmed[n] == ' '
}

// isListItem reports whether a whitespace-trimmed line begins a list item —
// an unordered marker ('-', '*', '+') or an ordered marker (digits then '.' or
// ')') followed by a space.
func isListItem(trimmed string) bool {
	if len(trimmed) >= 2 &&
		(trimmed[0] == '-' || trimmed[0] == '*' || trimmed[0] == '+') &&
		trimmed[1] == ' ' {
		return true
	}
	i := 0
	for i < len(trimmed) && trimmed[i] >= '0' && trimmed[i] <= '9' {
		i++
	}
	if i > 0 && i < len(trimmed) && (trimmed[i] == '.' || trimmed[i] == ')') {
		return i+1 < len(trimmed) && trimmed[i+1] == ' '
	}
	return false
}

// isIndented reports whether a line starts with a space or tab, marking it a
// candidate continuation of the current list item.
func isIndented(line string) bool {
	return len(line) > 0 && (line[0] == ' ' || line[0] == '\t')
}
