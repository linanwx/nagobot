// Frontmatter helpers — the ONLY sanctioned way in this codebase to
// parse, edit, or build YAML-frontmatter strings of the form:
//
//	---
//	<yaml mapping>
//	---
//	<body>
//
// Direct string concatenation of `---` fences with yaml.Marshal output is
// forbidden — every previous bug in this area (multi-line block scalars
// leaking, body content mistakenly merged into YAML, nested frontmatter
// triggering the wrong fence) traced back to ad-hoc concat or line-by-line
// parsing. New code MUST go through these helpers.
//
// Nested frontmatter: a body may itself begin with another `---\n...---\n`
// block (e.g. when one session quotes another's wake payload). The OUTER
// frontmatter is the only one parsed; the inner is preserved verbatim as
// body text. SplitFrontmatter / ParseFrontmatter close on the FIRST line
// equal to `---` after the opening fence, never on indented `---` strings
// inside YAML block scalars.

package msg

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// SplitFrontmatter locates the outer `---` fences and returns the raw YAML
// block between them and the body after the closing fence. This is a
// fence-locator only — it does NOT parse the YAML. For parsed access prefer
// ParseFrontmatter.
//
// The closing fence is the first occurrence of "\n---\n" after the opening
// `---\n`. Lines inside YAML block scalars are indented (or otherwise not a
// bare `---`), so they never match.
func SplitFrontmatter(content string) (yamlBlock, body string, ok bool) {
	if !strings.HasPrefix(content, "---\n") {
		return "", content, false
	}
	rest := content[4:]
	idx := strings.Index(rest, "\n---\n")
	if idx < 0 {
		// Allow a closing fence with no trailing newline (`...---` at EOF).
		if strings.HasSuffix(rest, "\n---") {
			return rest[:len(rest)-4], "", true
		}
		return "", content, false
	}
	return rest[:idx], rest[idx+5:], true
}

// ParseFrontmatter parses the OUTER YAML frontmatter and returns the root
// mapping node plus the verbatim body. Returns ok=false when there is no
// frontmatter, when YAML parsing fails, or when the root is not a mapping.
//
// The body is preserved verbatim — if it itself starts with another
// `---\n...---\n` (nested frontmatter), that inner block is left untouched.
func ParseFrontmatter(content string) (mapping *yaml.Node, body string, ok bool) {
	yamlBlock, body, ok := SplitFrontmatter(content)
	if !ok {
		return nil, body, false
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(yamlBlock), &doc); err != nil {
		return nil, body, false
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, body, false
	}
	return doc.Content[0], body, true
}

// BuildFrontmatter is the ONLY sanctioned way to construct a frontmatter
// string. It marshals the given mapping node, wraps it in `---` fences, and
// appends body verbatim. The closing fence is always followed by a single
// newline; the body comes after that with no further injection — pass body
// with a leading "\n" yourself if you want a visual blank line between the
// fence and the body.
//
// If mapping is nil or not a mapping, an empty `---\n---\n` header is emitted.
func BuildFrontmatter(mapping *yaml.Node, body string) string {
	yamlText := marshalMappingText(mapping)
	var sb strings.Builder
	sb.Grow(len(yamlText) + len(body) + 8)
	sb.WriteString("---\n")
	sb.WriteString(yamlText)
	sb.WriteString("---\n")
	sb.WriteString(body)
	return sb.String()
}

func marshalMappingText(mapping *yaml.Node) string {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return ""
	}
	if len(mapping.Content) == 0 {
		return ""
	}
	out, err := yaml.Marshal(&yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{mapping}})
	if err != nil {
		return ""
	}
	s := string(out)
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return s
}

// NewMapping returns a fresh empty mapping node, suitable for use with the
// other helpers (AppendScalarPair, BuildFrontmatter, ...).
func NewMapping() *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode}
}

// EncodeMapping creates a mapping node from a Go value via yaml.Node.Encode.
// The value is typically a struct with `yaml:"..."` tags. Returns ok=false on
// encode failure or when the result is not a mapping.
func EncodeMapping(v any) (*yaml.Node, bool) {
	var node yaml.Node
	if err := node.Encode(v); err != nil {
		return nil, false
	}
	if node.Kind != yaml.MappingNode {
		return nil, false
	}
	return &node, true
}

// AppendScalarPair appends a scalar key/value pair to a mapping node.
// Untyped scalar values (no Tag) let yaml.Marshal emit native types
// (`compressed: true`, `original: 4126`) without quoting.
func AppendScalarPair(mapping *yaml.Node, key, value string) {
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Value: value},
	)
}

// AppendValue appends a key/Go-value pair to a mapping node. The value is
// encoded via yaml.Node.Encode so booleans, ints, and other types emit as
// native YAML scalars. Strings that need quoting are quoted automatically.
func AppendValue(mapping *yaml.Node, key string, value any) error {
	var v yaml.Node
	if err := v.Encode(value); err != nil {
		return fmt.Errorf("encode %q: %w", key, err)
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&v,
	)
	return nil
}

// LookupScalar returns the scalar value of `key` in a mapping node, or "".
func LookupScalar(mapping *yaml.Node, key string) string {
	if mapping == nil {
		return ""
	}
	pairs := mapping.Content
	for i := 0; i+1 < len(pairs); i += 2 {
		k, v := pairs[i], pairs[i+1]
		if k.Kind == yaml.ScalarNode && k.Value == key && v.Kind == yaml.ScalarNode {
			return v.Value
		}
	}
	return ""
}

// DropKeys removes top-level keys listed in trimKeys from a mapping node.
func DropKeys(mapping *yaml.Node, trimKeys map[string]bool) {
	if mapping == nil {
		return
	}
	pairs := mapping.Content
	kept := pairs[:0:0]
	for i := 0; i+1 < len(pairs); i += 2 {
		k, v := pairs[i], pairs[i+1]
		if k.Kind == yaml.ScalarNode && trimKeys[k.Value] {
			continue
		}
		kept = append(kept, k, v)
	}
	mapping.Content = kept
}

// HasKeyValue reports whether the mapping contains a top-level key whose
// scalar value equals the given target. Returns false for non-scalar values.
func HasKeyValue(mapping *yaml.Node, key, target string) bool {
	return LookupScalar(mapping, key) == target
}

// HasFrontmatterKeyValue is the convenience function combining ParseFrontmatter
// and HasKeyValue — used by code that just needs a single boolean check.
func HasFrontmatterKeyValue(content, key, target string) bool {
	mapping, _, ok := ParseFrontmatter(content)
	if !ok {
		return false
	}
	return HasKeyValue(mapping, key, target)
}

// ExtractFrontmatterValue returns the scalar value of a top-level key from
// raw YAML-block bytes (output of SplitFrontmatter). Returns "" when the key
// is missing, when the value is not a scalar, or when parsing fails.
func ExtractFrontmatterValue(yamlBlock, key string) string {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(yamlBlock), &doc); err != nil {
		return ""
	}
	if len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return ""
	}
	return LookupScalar(doc.Content[0], key)
}

// ExtractFrontmatterValueFromContent combines SplitFrontmatter and
// ExtractFrontmatterValue. Use when callers have full content (with fences)
// and only need a single scalar.
func ExtractFrontmatterValueFromContent(content, key string) string {
	mapping, _, ok := ParseFrontmatter(content)
	if !ok {
		return ""
	}
	return LookupScalar(mapping, key)
}

// IsInjectedUserMessage reports whether a wake-payload user message was
// injected mid-turn, marked via `injected: true` in the frontmatter.
func IsInjectedUserMessage(content string) bool {
	return ExtractFrontmatterValueFromContent(content, "injected") == "true"
}

// SortedFieldsMapping builds a mapping node with fixed leading keys (in the
// order given) followed by the remaining keys of `extra` in sorted order.
// Values are encoded via yaml.Node.Encode so native types stay native.
//
// Use this for tool/cmd result formatters that historically built YAML by
// concatenating sorted lines — now the concat happens inside yaml.Marshal.
func SortedFieldsMapping(leading [][2]string, extra map[string]any) (*yaml.Node, error) {
	mapping := NewMapping()
	for _, kv := range leading {
		AppendScalarPair(mapping, kv[0], kv[1])
	}
	if len(extra) > 0 {
		keys := make([]string, 0, len(extra))
		for k := range extra {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err := AppendValue(mapping, k, extra[k]); err != nil {
				return nil, err
			}
		}
	}
	return mapping, nil
}
