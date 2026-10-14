package scrub

// mapStrings visits values, never object keys, including TOML arrays of tables.
func mapStrings(node any, transform func(string) string) any {
	switch n := node.(type) {
	case string:
		return transform(n)
	case map[string]any:
		for key, value := range n {
			n[key] = mapStrings(value, transform)
		}
	case []any:
		for i, value := range n {
			n[i] = mapStrings(value, transform)
		}
	case []map[string]any:
		for _, table := range n {
			mapStrings(table, transform)
		}
	}
	return node
}

func replaceCopies(doc any, redactions map[string]string) any {
	return mapStrings(doc, func(value string) string {
		if replacement, ok := redactions[value]; ok {
			return replacement
		}
		return value
	})
}

func noSecretCopies(f format, data []byte, redactions map[string]string) bool {
	doc, err := decode(f, data)
	if err != nil {
		return false
	}
	found := false
	mapStrings(doc, func(value string) string {
		if _, ok := redactions[value]; ok {
			found = true
		}
		return value
	})
	return !found
}
