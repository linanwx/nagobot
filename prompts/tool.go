package prompts

import (
	"sort"
	"strings"
)

// DeclareTool registers the text of one tool definition, read from
// tools/<name>.md: the body is the tool description, and the frontmatter's
// `params` map gives each schema property's description by dotted path
// ("sends.to" is the `to` property of the items of the `sends` array).
func DeclareTool(name string, opts ...Option) *Prompt {
	opts = append(opts, func(p *Prompt) { p.tool = true })
	return Declare("tools/"+name, opts...)
}

// Describe fills a tool's description and its parameter descriptions into
// schema. The schema keeps its structure in code — types, enums, required
// lists, everything validation depends on — and only the prose comes from
// the file. A property with no description in the file, or a description with
// no property, is logged at Error: either way the model would be shown a
// schema that disagrees with what the file's author wrote.
func (p *Prompt) Describe(schema map[string]any, vars Vars) string {
	f := active().text(p.key)
	used := map[string]bool{}
	describeProperties(p, schema, "", f.params, vars, used)
	var unused []string
	for k := range f.params {
		if !used[k] {
			unused = append(unused, k)
		}
	}
	if len(unused) > 0 {
		sort.Strings(unused)
		reportError("tool prompt describes parameters the schema does not have", "key", p.key, "params", strings.Join(unused, ","))
	}
	return fill(p, f.body, vars)
}

func describeProperties(p *Prompt, node map[string]any, prefix string, params map[string]string, vars Vars, used map[string]bool) {
	props, _ := node["properties"].(map[string]any)
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		prop, ok := props[name].(map[string]any)
		if !ok {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		if desc, ok := params[path]; ok {
			prop["description"] = fill(p, desc, vars)
			used[path] = true
		} else {
			reportError("tool parameter has no description in its prompt file", "key", p.key, "param", path)
		}
		describeProperties(p, prop, path, params, vars, used)
		if items, ok := prop["items"].(map[string]any); ok {
			describeProperties(p, items, path, params, vars, used)
		}
	}
}

// ToolParams is used by the extraction tooling and tests: the parameter
// descriptions a tool file carries, by dotted path.
func (p *Prompt) ToolParams() map[string]string {
	out := map[string]string{}
	for k, v := range active().text(p.key).params {
		out[k] = v
	}
	return out
}
