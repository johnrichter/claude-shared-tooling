package state

// RegistrySchemaVersion is the current schema version of a source-registry document.
const RegistrySchemaVersion = 1

// registryMigrations is the (currently empty) upgrade chain for source-registry
// documents — extend it the same way store.go's Migrations are extended, the first time
// RegistrySchemaVersion moves past 1.
var registryMigrations = Migrations{}

// SourceEntry is one ref's registration record: when it was first seen, every consumer
// that has seen it, and when it was last seen by any of them.
type SourceEntry struct {
	FirstSeen string   `json:"first_seen"`
	Consumers []string `json:"consumers"`
	LastSeen  string   `json:"last_seen"`
}

// readRefs decodes doc's "refs" field into a typed map, tolerating a missing or
// malformed field (an empty map) rather than raising — Read has already degraded the
// surrounding document, and a registry that fails to parse its own refs must degrade the
// same way, not propagate a type-assertion panic.
func readRefs(doc Doc) map[string]SourceEntry {
	refs := map[string]SourceEntry{}
	raw, ok := doc["refs"].(map[string]any)
	if !ok {
		return refs
	}
	for ref, v := range raw {
		entry, ok := v.(map[string]any)
		if !ok {
			continue
		}
		e := SourceEntry{}
		if s, ok := entry["first_seen"].(string); ok {
			e.FirstSeen = s
		}
		if s, ok := entry["last_seen"].(string); ok {
			e.LastSeen = s
		}
		if list, ok := entry["consumers"].([]any); ok {
			for _, c := range list {
				if s, ok := c.(string); ok {
					e.Consumers = append(e.Consumers, s)
				}
			}
		}
		refs[ref] = e
	}
	return refs
}

// writeRefs installs refs back into doc under "refs", in the shape Read/readRefs expects.
func writeRefs(doc Doc, refs map[string]SourceEntry) {
	raw := map[string]any{}
	for ref, e := range refs {
		raw[ref] = map[string]any{
			"first_seen": e.FirstSeen,
			"consumers":  e.Consumers,
			"last_seen":  e.LastSeen,
		}
	}
	doc["refs"] = raw
}

// RegisterSource records ref as seen by consumer in the registry at path, creating the
// registry (and the ref's entry) if either is new. Re-registering the same ref from the
// same or a different consumer is idempotent: consumer is added to the existing entry's
// consumer list only if not already present, first_seen is left untouched, and last_seen
// advances to at. The read-modify-write cycle runs under WithLock, so concurrent consumers
// — in this process or another — sharing one registry file dedupe against each other's
// writes instead of racing a plain read-then-write and losing updates.
func RegisterSource(path, ref, consumer, at string) error {
	return WithLock(path, func() error {
		doc, err := Read(path, RegistrySchemaVersion, registryMigrations)
		if err != nil {
			return err
		}
		refs := readRefs(doc)
		entry, exists := refs[ref]
		if !exists {
			entry = SourceEntry{FirstSeen: at}
		}
		seen := false
		for _, c := range entry.Consumers {
			if c == consumer {
				seen = true
				break
			}
		}
		if !seen {
			entry.Consumers = append(entry.Consumers, consumer)
		}
		entry.LastSeen = at
		refs[ref] = entry
		writeRefs(doc, refs)
		return Write(path, doc, 0o644)
	})
}

// SeenSource reports whether ref has been registered by any consumer in the registry at
// path. A missing or corrupt registry degrades to false — an unseen ref, never an error —
// consistent with Read's safe-degradation contract.
func SeenSource(path, ref string) bool {
	doc, err := Read(path, RegistrySchemaVersion, registryMigrations)
	if err != nil {
		return false
	}
	_, ok := readRefs(doc)[ref]
	return ok
}
