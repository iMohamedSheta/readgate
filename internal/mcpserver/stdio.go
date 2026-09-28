package mcpserver

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"readgate/internal/applog"
	"readgate/internal/store"
)

// Run starts the MCP stdio loop (goals parity): the same binary runs as
// `ReadGate.exe mcp`, speaking JSON-RPC 2.0 NDJSON on stdin/stdout.
// opencode config: { "mcp": { "readgate": {
//   "type": "local", "command": ["<exe>", "mcp"], "enabled": true } } }
// Protocol version: 2024-11-05. Nothing but JSON-RPC goes to stdout.
func Run() int {
	applog.Init(store.AppDir())
	st, err := store.New()
	if err != nil {
		fmt.Fprintf(os.Stderr, "readgate mcp: open db: %v\n", err)
		return 1
	}
	defer st.Close()
	srv := New(st)

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1024*1024), 1024*1024*10)
	out := bufio.NewWriter(os.Stdout)
	defer out.Flush()

	write := func(id any, result any, rerr *rpcErr) {
		resp := rpcResp{JSONRPC: "2.0", ID: id, Result: result, Error: rerr}
		b, _ := json.Marshal(resp)
		b = append(b, '\n')
		_, _ = out.Write(b)
		_ = out.Flush()
	}

	for in.Scan() {
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		var req rpcReq
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		if req.JSONRPC == "" {
			req.JSONRPC = "2.0"
		}
		isNotification := req.ID == nil

		switch req.Method {
		case "initialize":
			write(req.ID, map[string]any{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
				"serverInfo":      map[string]any{"name": "readgate", "version": "0.1.0"},
			}, nil)
		case "notifications/initialized", "notifications/cancelled", "logging/setLevel":
			// no-op (notifications get no reply)
		case "ping":
			if !isNotification {
				write(req.ID, map[string]any{}, nil)
			}
		case "tools/list":
			write(req.ID, map[string]any{"tools": toolList()}, nil)
		case "prompts/list":
			write(req.ID, map[string]any{"prompts": []any{}}, nil)
		case "resources/list":
			write(req.ID, map[string]any{"resources": []any{}}, nil)
		case "resources/templates/list":
			write(req.ID, map[string]any{"resourceTemplates": []any{}}, nil)
		case "tools/call":
			var p struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			if p.Name == "" {
				// some clients nest as params.arguments directly
				var alt map[string]any
				_ = json.Unmarshal(req.Params, &alt)
				if n, ok := alt["name"].(string); ok {
					p.Name = n
					if a, ok := alt["arguments"].(map[string]any); ok {
						p.Arguments = a
					}
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			out2, err := srv.callTool(ctx, p.Name, p.Arguments)
			cancel()
			if err != nil {
				write(req.ID, map[string]any{
					"content": []any{map[string]any{"type": "text", "text": "error: " + err.Error()}},
					"isError": true,
				}, nil)
			} else {
				text, _ := json.MarshalIndent(out2, "", "  ")
				write(req.ID, map[string]any{
					"content": []any{map[string]any{"type": "text", "text": string(text)}},
				}, nil)
			}
		default:
			if !isNotification {
				write(req.ID, nil, &rpcErr{Code: -32601, Message: "method not found: " + req.Method})
			}
		}
	}
	return 0
}
