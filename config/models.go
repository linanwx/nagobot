package config

// FindModelRule returns the first rule matching (typ, name), or nil.
// Linear scan is intentional: the rule list is small and a single resolution
// does at most a few lookups, so an index would be premature.
func FindModelRule(rules []ModelRule, typ, name string) *ModelRule {
	for i := range rules {
		if rules[i].Type == typ && rules[i].Name == name {
			return &rules[i]
		}
	}
	return nil
}

// UpsertModelRule overwrites the provider/model of an existing (typ, name) rule
// in place, or appends a new rule if none matches. Names are unique within a
// type, so there is at most one match.
func UpsertModelRule(rules []ModelRule, typ, name, provider, modelType string) []ModelRule {
	for i := range rules {
		if rules[i].Type == typ && rules[i].Name == name {
			rules[i].Provider = provider
			rules[i].ModelType = modelType
			return rules
		}
	}
	return append(rules, ModelRule{Type: typ, Name: name, Provider: provider, ModelType: modelType})
}

// RemoveModelRule drops the rule matching (typ, name), returning the filtered
// slice. No-op if no rule matches.
func RemoveModelRule(rules []ModelRule, typ, name string) []ModelRule {
	out := rules[:0]
	for _, r := range rules {
		if r.Type == typ && r.Name == name {
			continue
		}
		out = append(out, r)
	}
	return out
}
