package schema

import "strings"

// TagNamespaces groups a document's frontmatter tags by their declared namespace: the substring
// before the first ':' in a "namespace:value" tag. A tag with no ':' is grouped under the
// empty-string namespace. Within each namespace, values keep the order they appear in tags.
func TagNamespaces(tags []string) map[string][]string {
	groups := make(map[string][]string)
	for _, tag := range tags {
		ns, value, found := strings.Cut(tag, ":")
		if !found {
			ns, value = "", tag
		}
		groups[ns] = append(groups[ns], value)
	}
	return groups
}
