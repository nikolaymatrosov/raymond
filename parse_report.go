package raymond

// ParseReport is the read-only observability snapshot for a successful parse
// performed via ParseWithOptions.
//
// Substitutions counts substitution-producing mustache nodes (every
// MustacheStatement that yields output, including triple-stash and
// whitespace-control variants; comments and block/partial openers are
// excluded).
//
// Constructs is a sorted, deduplicated subset of the closed vocabulary
// {"if","unless","each","with","partial","helper"}. Plain substitution is
// not a "construct" — it is reflected in Substitutions.
type ParseReport struct {
	Substitutions int
	Constructs    []string
}
