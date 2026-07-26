package graph

import "strings"

// Claim kinds understood by NamespaceDomain.
const (
	// NameExact is one name, and nothing below it.
	NameExact = "name"
	// NamePrefix is a name together with everything below it.
	NamePrefix = "prefix"
)

// NamespaceOption configures a NamespaceDomain.
type NamespaceOption func(*namespaceConfig)

type namespaceConfig struct {
	separator string
	fold      func(string) string
}

// WithNamespaceSeparator sets the string that divides one level of the
// namespace from the next, defaulting to "/". Use "." for a dotted namespace
// such as a Java package or a message topic, and "" for a flat namespace with
// no hierarchy, where NamePrefix then means only the name itself.
func WithNamespaceSeparator(sep string) NamespaceOption {
	return func(c *namespaceConfig) { c.separator = sep }
}

// WithNamespaceFold applies fold to both sides of every comparison. Supply one
// where the namespace itself is case-insensitive or otherwise treats different
// spellings as one name; folding merges names, which costs precision, whereas
// leaving two spellings of one name apart would wrongly prove them disjoint.
func WithNamespaceFold(fold func(string) string) NamespaceOption {
	return func(c *namespaceConfig) { c.fold = fold }
}

// NamespaceDomain returns a resource domain over hierarchical names,
// registered under name — the generalisation of a file surface to everything a
// unit of work can contend over that is not a path: a package or module whose
// symbols must not collide, a lock, a queue or topic, a table, a feature flag,
// a service endpoint.
//
// It decides claims of kind NameExact and NamePrefix, treating any other kind
// as undecidable. Two exact names overlap when they are equal; a prefix
// overlaps a name it is a prefix of at a separator boundary, so "app/store"
// contains "app/store/index" but not "app/storefront". Comparison is exact by
// default, because in a namespace two spellings really are two resources;
// WithNamespaceFold covers the namespaces where they are not.
func NamespaceDomain(name string, opts ...NamespaceOption) Domain {
	cfg := namespaceConfig{separator: "/"}
	for _, opt := range opts {
		opt(&cfg)
	}
	return Domain{
		Name:   name,
		Relate: func(a, b Claim) Relation { return cfg.relate(a, b) },
	}
}

// relate decides how two namespace claims stand to one another.
func (c namespaceConfig) relate(a, b Claim) Relation {
	va, okA := c.resolve(a)
	vb, okB := c.resolve(b)
	if !okA || !okB {
		return RelationUnknown
	}
	switch {
	case a.Kind == NamePrefix && b.Kind == NamePrefix:
		return overlapIf(c.contains(va, vb) || c.contains(vb, va))
	case a.Kind == NamePrefix:
		return overlapIf(c.contains(va, vb))
	case b.Kind == NamePrefix:
		return overlapIf(c.contains(vb, va))
	default:
		return overlapIf(va == vb)
	}
}

// resolve normalises and folds a claim, reporting false for an unknown kind or
// an empty name.
func (c namespaceConfig) resolve(cl Claim) (string, bool) {
	switch cl.Kind {
	case NameExact, NamePrefix:
	default:
		return "", false
	}
	v := strings.TrimSpace(cl.Value)
	if c.separator != "" {
		v = strings.TrimSuffix(v, c.separator)
	}
	if v == "" {
		return "", false
	}
	if c.fold != nil {
		v = c.fold(v)
	}
	return v, true
}

// contains reports whether prefix covers name: the same name, or one below it
// at a separator boundary. A flat namespace has nothing below anything, so a
// prefix there covers only itself.
func (c namespaceConfig) contains(prefix, name string) bool {
	if prefix == name {
		return true
	}
	return c.separator != "" && strings.HasPrefix(name, prefix+c.separator)
}
