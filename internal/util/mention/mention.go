// Package mention parses inline mention markdown out of timeline content.
//
// Mention syntax (kept identical to multica's so any tool that emits a multica
// comment will render the same on octo-matter):
//
//   [@DisplayName](mention://kind/{id})
//
// kind ∈ {"agent", "member", "squad", "issue", "all"}. For PoC3c we only act
// on "agent" mentions — that's what triggers a runtime dispatch. The other
// kinds are still parsed (so the UI can render chips for them) but produce
// no side effects on the server side yet.
package mention

import (
	"regexp"
	"strings"
)

// Kind is the discriminator inside mention://<kind>/<id>.
type Kind string

const (
	KindAgent  Kind = "agent"
	KindMember Kind = "member"
	KindSquad  Kind = "squad"
)

// Mention is one parsed [@Label](mention://kind/id) hit. Position is the
// byte offset of the opening `[` in the source content — handy if callers
// want to splice the chip into a render tree later.
type Mention struct {
	Kind     Kind
	ID       string
	Label    string
	Position int
}

// mentionRe matches `[@anything-but-]](mention://kind/id)`. The label segment
// allows any non-`]` character (including unicode/spaces) so display names
// like "齐乐" work. The id segment is restricted to URL-safe characters to
// keep the regex bounded.
var mentionRe = regexp.MustCompile(`\[@([^\]]+)\]\(mention://([a-z]+)/([A-Za-z0-9_\-]+)\)`)

// Parse returns every mention in content, deduplicated by (kind, id) — the
// first occurrence wins on label. Empty result for empty input or no matches.
// Stable order matches source position.
func Parse(content string) []Mention {
	if content == "" {
		return nil
	}
	hits := mentionRe.FindAllStringSubmatchIndex(content, -1)
	if len(hits) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(hits))
	out := make([]Mention, 0, len(hits))
	for _, h := range hits {
		// h indices: 0,1 = full match; 2,3 = label; 4,5 = kind; 6,7 = id
		label := content[h[2]:h[3]]
		kind := Kind(strings.ToLower(content[h[4]:h[5]]))
		id := content[h[6]:h[7]]
		key := string(kind) + ":" + id
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, Mention{
			Kind:     kind,
			ID:       id,
			Label:    label,
			Position: h[0],
		})
	}
	return out
}

// AgentIDs returns just the agent-kind ids from parsed mentions, in source
// order, deduplicated. Convenience for the dispatch hook which only cares
// about "which bots did this comment ping".
func AgentIDs(mentions []Mention) []string {
	if len(mentions) == 0 {
		return nil
	}
	out := make([]string, 0, len(mentions))
	for _, m := range mentions {
		if m.Kind == KindAgent {
			out = append(out, m.ID)
		}
	}
	return out
}
