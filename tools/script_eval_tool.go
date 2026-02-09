package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/linanwx/nagobot/provider"
	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

const (
	scriptEvalDefaultTimeoutSeconds = 30
	scriptEvalOutputMaxChars        = 50000
)

// ScriptEvalTool evaluates Go code using an in-process interpreter (Yaegi).
type ScriptEvalTool struct {
	workspace string
}

// Def returns the tool definition.
func (t *ScriptEvalTool) Def() provider.ToolDef {
	return provider.ToolDef{
		Type: "function",
		Function: provider.FunctionDef{
			Name: "run_go_code",
			Description: "Execute Go code using an in-process interpreter. " +
				"The code must be a complete Go program with package main and func main(). " +
				"Standard library is available (fmt, os, encoding/json, strings, etc). " +
				"Use fmt.Println() to produce output. Timeout defaults to 30 seconds.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"code": map[string]any{
						"type":        "string",
						"description": "Complete Go source code with package main and func main().",
					},
					"timeout": map[string]any{
						"type":        "integer",
						"description": "Optional timeout in seconds. Defaults to 30.",
					},
				},
				"required": []string{"code"},
			},
		},
	}
}

type scriptEvalArgs struct {
	Code    string `json:"code"`
	Timeout int    `json:"timeout,omitempty"`
}

// Run executes the tool.
func (t *ScriptEvalTool) Run(ctx context.Context, args json.RawMessage) string {
	var a scriptEvalArgs
	if errMsg := parseArgs(args, &a); errMsg != "" {
		return errMsg
	}

	timeout := a.Timeout
	if timeout <= 0 {
		timeout = scriptEvalDefaultTimeoutSeconds
	}

	evalCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	type evalResult struct {
		output string
		err    error
	}
	ch := make(chan evalResult, 1)

	go func() {
		out, err := runYaegi(a.Code, t.workspace)
		ch <- evalResult{output: out, err: err}
	}()

	select {
	case <-evalCtx.Done():
		return fmt.Sprintf("Error: code execution timed out after %d seconds", timeout)
	case res := <-ch:
		if res.err != nil {
			if res.output != "" {
				return fmt.Sprintf("Error: %v\nOutput:\n%s", res.err, res.output)
			}
			return fmt.Sprintf("Error: %v", res.err)
		}
		result := res.output
		if result == "" {
			return "(no output)"
		}
		result, _ = truncateWithNotice(result, scriptEvalOutputMaxChars)
		return result
	}
}

func runYaegi(code, workspace string) (string, error) {
	var buf bytes.Buffer

	i := interp.New(interp.Options{
		GoPath: workspace,
		Stdout: &buf,
		Stderr: &buf,
	})
	i.Use(stdlib.Symbols)

	_, err := i.Eval(code)
	return buf.String(), err
}
