package mcp

import (
	"encoding/json"
	"reflect"
	"strconv"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// ---- argument helpers ------------------------------------------------------

func strArg(args map[string]any, key string) string {
	v, _ := args[key].(string)
	return v
}

func intArg(args map[string]any, key string) int {
	switch v := args[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, _ := strconv.Atoi(v)
		return n
	}
	return 0
}

func boolArg(args map[string]any, key string) bool {
	v, _ := args[key].(bool)
	return v
}

func strSliceArg(args map[string]any, key string) []string {
	raw, _ := args[key].([]any)
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		if s, ok := r.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// durationArg accepts "30s", "2m" or a bare number meaning seconds.
func durationArg(args map[string]any, key string) time.Duration {
	v := strArg(args, key)
	if v == "" {
		return 0
	}
	if d, err := time.ParseDuration(v); err == nil {
		return d
	}
	if n, err := strconv.Atoi(v); err == nil {
		return time.Duration(n) * time.Second
	}
	return 0
}

// ---- result helpers --------------------------------------------------------

// textResult returns pretty JSON both as text content and as structured
// content, so MCP clients get the machine-readable form for free.
//
// StructuredContent must be a JSON object per the MCP spec; arrays are sent
// as text only, otherwise strict clients (opencode validates the field) reject
// the result with "expected record, received array".
func textResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	res := &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent(string(b))},
	}
	if isObject(v) {
		res.StructuredContent = v
	}
	return res, nil
}

// isObject reports whether v marshals to a JSON object, i.e. it is safe to
// put into StructuredContent. Slices, arrays and scalars are not objects.
func isObject(v any) bool {
	rv := reflect.ValueOf(v)
	for rv.Kind() == reflect.Ptr || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return false
		}
		rv = rv.Elem()
	}
	switch rv.Kind() {
	case reflect.Map, reflect.Struct:
		return true
	}
	return false
}

// errResult reports a tool-level error the LLM can see and self-correct from.
func errResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{mcp.NewTextContent("Error: " + err.Error())},
		IsError: true,
	}
}
