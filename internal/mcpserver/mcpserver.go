package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"readgate/internal/guard"
	"readgate/internal/model"
	"readgate/internal/dbops"
	"readgate/internal/store"
)

// Server exposes a minimal MCP-compatible HTTP endpoint on loopback
// plus plain REST the desktop UI uses. The AI never sees credentials:
// tools accept only source NAMES.
type Server struct {
	st       *store.Store
	mu       sync.Mutex
	http     *http.Server
	listener net.Listener
	addr     string
	running  bool
}

func New(st *store.Store) *Server { return &Server{st: st} }

func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}
func (s *Server) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}
type rpcResp struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      interface{} `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *rpcErr     `json:"error,omitempty"`
}
type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolDef struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

func toolList() []toolDef {
	obj := func(props map[string]any, req []string) map[string]any {
		if req == nil {
			req = []string{}
		}
		return map[string]any{"type": "object", "properties": props, "required": req}
	}
	str := map[string]any{"type": "string"}
	int := map[string]any{"type": "integer"}
	return []toolDef{
		{"fleet_overview", "Fleet map: clusters and source names the AI may use. No credentials. Start every session here.", obj(map[string]any{}, []string{})},
		{"list_sources", "List enabled source names (grouped by cluster).", obj(map[string]any{}, []string{})},
		{"list_clusters", "List clusters.", obj(map[string]any{}, []string{})},
		{"schema", "Table NAMES + row estimates for one source (fast). Follow with columns() per table.", obj(map[string]any{"source": str}, []string{"source"})},
		{"columns", "Column inventory for one table.", obj(map[string]any{"source": str, "table": str}, []string{"source", "table"})},
		{"query", "Run a READ-ONLY SELECT/WITH/EXPLAIN. Writes are rejected. Max 500 rows.", obj(map[string]any{"source": str, "sql": str, "limit": int}, []string{"source", "sql"})},
		{"explain", "EXPLAIN a query to reason about slowness.", obj(map[string]any{"source": str, "sql": str}, []string{"source", "sql"})},
		{"sample", "Sample N rows from a table (read-only).", obj(map[string]any{"source": str, "table": str, "limit": int}, []string{"source", "table"})},
		{"doctor", "Read-only health findings: seq scans, missing PKs, connection pressure, long queries.", obj(map[string]any{"source": str}, []string{"source"})},
		{"table_stats", "Size + row estimate + seq/index scans + dead tuples for one table, or the biggest tables when table is omitted.", obj(map[string]any{"source": str, "table": str, "limit": int}, []string{"source"})},
		{"indexes", "Index inventory (name + definition) for one table.", obj(map[string]any{"source": str, "table": str}, []string{"source", "table"})},
		{"relationships", "Foreign-key graph: from table.column → referenced table.column. Omit table for the whole map (capped).", obj(map[string]any{"source": str, "table": str, "limit": int}, []string{"source"})},
		{"search_tables", "Fuzzy-find tables by name fragment across schemas.", obj(map[string]any{"source": str, "pattern": str, "limit": int}, []string{"source", "pattern"})},
		{"slow_queries", "Heaviest statements via pg_stat_statements (falls back to running queries).", obj(map[string]any{"source": str, "limit": int}, []string{"source"})},
	}
}

// splitTable accepts "table" or "schema.table" and defaults schema to public.
func splitTable(table string) (string, string) {
	schema, name := "public", table
	if i := strings.LastIndex(table, "."); i >= 0 {
		schema, name = table[:i], table[i+1:]
	}
	return schema, name
}

func (s *Server) findSource(name string) (model.Source, error) {
	for _, src := range s.st.ListSources() {
		if src.Name == name {
			if src.Status != model.StatusReady || !src.ReadOnlyVerified {
				return model.Source{}, fmt.Errorf("source %q is not enabled (status=%s, verified=%v) — enable it in ReadGate first", name, src.Status, src.ReadOnlyVerified)
			}
			return src, nil
		}
	}
	return model.Source{}, fmt.Errorf("unknown source %q", name)
}

func (s *Server) fleetOverview() model.FleetOverview {
	clusters := s.st.ListClusters()
	cname := map[string]string{}
	for _, c := range clusters {
		cname[c.ID] = c.Name
	}
	var pubs []model.SourcePublic
	for _, src := range s.st.ListSources() {
		pubs = append(pubs, model.SourcePublic{
			Name: src.Name, Cluster: cname[src.ClusterID], Engine: string(src.Engine),
			Database: src.Database, Status: string(src.Status), ReadOnly: src.ReadOnlyVerified,
		})
	}
	return model.FleetOverview{Clusters: clusters, Sources: pubs}
}

func (s *Server) callTool(ctx context.Context, name string, args map[string]any) (any, error) {
	str := func(k string) string {
		if v, ok := args[k].(string); ok {
			return v
		}
		return ""
	}
	num := func(k string, d int) int {
		if v, ok := args[k].(float64); ok && v > 0 {
			return int(v)
		}
		return d
	}
	switch name {
	case "fleet_overview":
		return s.fleetOverview(), nil
	case "list_sources", "list_clusters":
		return s.fleetOverview(), nil
	case "schema":
		src, err := s.findSource(str("source"))
		if err != nil {
			return nil, err
		}
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return dbops.Schema(c, src)
	case "columns":
		src, err := s.findSource(str("source"))
		if err != nil {
			return nil, err
		}
		table := str("table")
		schema, name := "public", table
		if i := strings.LastIndex(table, "."); i >= 0 {
			schema, name = table[:i], table[i+1:]
		}
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		cols, err := dbops.Columns(c, src, schema, name)
		if err != nil {
			return nil, err
		}
		return map[string]any{"source": src.Name, "table": schema + "." + name, "columns": cols}, nil
	case "query":
		src, err := s.findSource(str("source"))
		if err != nil {
			return nil, err
		}
		sql := str("sql")
		if err := guard.ValidateReadOnlySQL(sql); err != nil {
			return nil, err
		}
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return dbops.Query(c, src, sql, num("limit", 200))
	case "explain":
		src, err := s.findSource(str("source"))
		if err != nil {
			return nil, err
		}
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return map[string]string{"plan": must(dbops.Explain(c, src, str("sql"))) }, nil
	case "sample":
		src, err := s.findSource(str("source"))
		if err != nil {
			return nil, err
		}
		table := str("table")
		if table == "" {
			return nil, fmt.Errorf("table required")
		}
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		// table names are identifiers — quote safely
		safe := `"` + strRepl(table) + `"`
		// allow schema.table
		return dbops.Query(c, src, "SELECT * FROM "+safe, num("limit", 50))
	case "doctor":
		src, err := s.findSource(str("source"))
		if err != nil {
			return nil, err
		}
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return dbops.DoctorFindings(c, src)
	case "table_stats":
		src, err := s.findSource(str("source"))
		if err != nil {
			return nil, err
		}
		schema, name := splitTable(str("table"))
		only := str("table") != ""
		if !only {
			schema, name = "", ""
		}
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		stats, err := dbops.TableStats(c, src, schema, name, num("limit", 20))
		if err != nil {
			return nil, err
		}
		return map[string]any{"source": src.Name, "tables": stats}, nil
	case "indexes":
		src, err := s.findSource(str("source"))
		if err != nil {
			return nil, err
		}
		schema, name := splitTable(str("table"))
		if str("table") == "" {
			return nil, fmt.Errorf("table required")
		}
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		idx, err := dbops.Indexes(c, src, schema, name)
		if err != nil {
			return nil, err
		}
		return map[string]any{"source": src.Name, "table": schema + "." + name, "indexes": idx}, nil
	case "relationships":
		src, err := s.findSource(str("source"))
		if err != nil {
			return nil, err
		}
		schema, name := splitTable(str("table"))
		if str("table") == "" {
			schema, name = "", ""
		}
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		edges, err := dbops.Relationships(c, src, schema, name, num("limit", 100))
		if err != nil {
			return nil, err
		}
		return map[string]any{"source": src.Name, "foreignKeys": edges}, nil
	case "search_tables":
		src, err := s.findSource(str("source"))
		if err != nil {
			return nil, err
		}
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		matches, err := dbops.SearchTables(c, src, str("pattern"), num("limit", 20))
		if err != nil {
			return nil, err
		}
		return map[string]any{"source": src.Name, "matches": matches}, nil
	case "slow_queries":
		src, err := s.findSource(str("source"))
		if err != nil {
			return nil, err
		}
		c, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		return dbops.SlowQueries(c, src, num("limit", 10))
	}
	return nil, fmt.Errorf("unknown tool %q", name)
}

func strRepl(s string) string {
	out := ""
	for _, r := range s {
		if r == '"' {
			out += `""`
		} else {
			out += string(r)
		}
	}
	// keep dots for schema.table
	return out
}

func must(s string, err error) string {
	if err != nil {
		return "error: " + err.Error()
	}
	return s
}

// Start binds 127.0.0.1:0 (or preferred port) and serves MCP + REST.
func (s *Server) Start(preferredPort int) (string, error) {
	s.mu.Lock()
	if s.running {
		a := s.addr
		s.mu.Unlock()
		return a, nil
	}
	s.mu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", s.handleMCP)
	mux.HandleFunc("/mcp/tools", s.handleTools)
	mux.HandleFunc("/api/fleet", s.handleFleet)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true,"service":"readgate"}`))
	})

	addr := "127.0.0.1:9413"
	if preferredPort != 0 {
		addr = fmt.Sprintf("127.0.0.1:%d", preferredPort)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil && preferredPort != 0 {
		ln, err = net.Listen("tcp", "127.0.0.1:0")
	}
	if err != nil {
		// last resort ephemeral
		ln, err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return "", err
		}
	}
	srv := &http.Server{Handler: cors(mux), ReadHeaderTimeout: 10 * time.Second}
	s.mu.Lock()
	s.http = srv
	s.listener = ln
	s.addr = "http://" + ln.Addr().String()
	s.running = true
	s.mu.Unlock()
	go srv.Serve(ln)
	return s.addr, nil
}

func (s *Server) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.http != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		s.http.Shutdown(ctx)
	}
	s.running = false
}

func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Mcp-Session-Id")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == "OPTIONS" {
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (s *Server) handleTools(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"tools": toolList()})
}
func (s *Server) handleFleet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.fleetOverview())
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		writeJSON(w, map[string]any{
			"name": "readgate", "version": "0.1.0",
			"description": "AI-safe read-only database gateway. SSH tunnels + verified read-only users + fleet clusters.",
			"tools": toolList(),
		})
		return
	}
	var req rpcReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, rpcResp{JSONRPC: "2.0", Error: &rpcErr{Code: -32700, Message: "parse error"}})
		return
	}
	// JSON-RPC notifications carry no id — the MCP spec requires 202 + empty
	// body here. Answering 200 with an error object breaks strict clients
	// (e.g. opencode fails the whole handshake with "Failed to get tools").
	if req.ID == nil {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	switch req.Method {
	case "initialize":
		writeJSON(w, rpcResp{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "readgate", "version": "0.1.0"},
		}})
	case "ping":
		writeJSON(w, rpcResp{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}})
	case "tools/list":
		writeJSON(w, rpcResp{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": toolList()}})
	case "prompts/list":
		writeJSON(w, rpcResp{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"prompts": []any{}}})
	case "resources/list":
		writeJSON(w, rpcResp{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"resources": []any{}}})
	case "resources/templates/list":
		writeJSON(w, rpcResp{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"resourceTemplates": []any{}}})
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		_ = json.Unmarshal(req.Params, &p)
		out, err := s.callTool(ctx, p.Name, p.Arguments)
		if err != nil {
			writeJSON(w, rpcResp{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
				"content": []any{map[string]any{"type": "text", "text": "error: " + err.Error()}},
				"isError": true,
			}})
			return
		}
		text, _ := json.MarshalIndent(out, "", "  ")
		writeJSON(w, rpcResp{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"content": []any{map[string]any{"type": "text", "text": string(text)}},
		}})
	default:
		writeJSON(w, rpcResp{JSONRPC: "2.0", ID: req.ID, Error: &rpcErr{Code: -32601, Message: "unknown method " + req.Method}})
	}
}
