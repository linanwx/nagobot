package tools

import (
	"sort"
	"testing"

	"github.com/linanwx/nagobot/prompts"
)

// TestToolPromptFilesDescribeExactlyTheSchema sweeps every tool definition:
// each schema property must get its description from the tool's prompt file,
// and the file must describe no property the schema lacks. Either mismatch is
// only logged at runtime, and the model would be shown a schema that
// disagrees with what the file's author wrote.
func TestToolPromptFilesDescribeExactlyTheSchema(t *testing.T) {
	cases := map[string]struct {
		tool   Tool
		prompt *prompts.Prompt
	}{
		"check_session": {&CheckSessionTool{}, promptToolCheckSession},
		"dispatch":      {&DispatchTool{}, promptToolDispatch},
		"exec":          {&ExecTool{}, promptToolExec},
		"read_file":     {&ReadFileTool{}, promptToolReadFile},
		"write_file":    {&WriteFileTool{}, promptToolWriteFile},
		"edit_file":     {&EditFileTool{}, promptToolEditFile},
		"grep":          {&GrepTool{}, promptToolGrep},
		"glob":          {&GlobTool{}, promptToolGlob},
		"health":        {&HealthTool{}, promptToolHealth},
		"use_skill":     {&UseSkillTool{}, promptToolUseSkill},
		"web_search":    {&WebSearchTool{}, promptToolWebSearch},
		"web_fetch":     {&WebFetchTool{}, promptToolWebFetch},
	}
	for name, c := range cases {
		def := c.tool.Def()
		if def.Function.Name != name {
			t.Errorf("%s: Def names itself %q", name, def.Function.Name)
		}
		if def.Function.Description == "" {
			t.Errorf("%s: empty description", name)
		}
		var paths []string
		collectPropertyPaths(def.Function.Parameters, "", &paths)
		sort.Strings(paths)
		params := c.prompt.ToolParams()
		for _, p := range paths {
			if params[p] == "" {
				t.Errorf("%s: property %s has no description in tools/%s.md", name, p, name)
			}
			delete(params, p)
		}
		for p := range params {
			t.Errorf("%s: tools/%s.md describes %s, which the schema does not have", name, name, p)
		}
	}
}

func collectPropertyPaths(node map[string]any, prefix string, out *[]string) {
	props, _ := node["properties"].(map[string]any)
	for name, v := range props {
		prop, ok := v.(map[string]any)
		if !ok {
			continue
		}
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		*out = append(*out, path)
		collectPropertyPaths(prop, path, out)
		if items, ok := prop["items"].(map[string]any); ok {
			collectPropertyPaths(items, path, out)
		}
	}
}
