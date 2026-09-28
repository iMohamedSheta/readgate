package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"readgate/internal/mcpserver"
	"readgate/internal/model"
	"readgate/internal/dbops"
	"readgate/internal/engines"
	"readgate/internal/lite"
	"readgate/internal/mssql"
	"readgate/internal/my"
	"readgate/internal/pg"
	"readgate/internal/turso"
	"readgate/internal/provision"
	"readgate/internal/sshx"
	"readgate/internal/store"

	"readgate/internal/applog"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// App is the Wails binding surface. All secrets stay in Go;
// the frontend and the AI only ever see names + check results.
type App struct {
	ctx   context.Context
	store *store.Store
	mcp   *mcpserver.Server
	mcpAddr string
}

func NewApp() (*App, error) {
	applog.Init(store.AppDir())
	st, err := store.New()
	if err != nil {
		applog.Error("store.init", err)
		return nil, err
	}
	applog.Info("readgate started")
	return &App{store: st, mcp: mcpserver.New(st)}, nil
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	// auto-start MCP on loopback so opencode works out of the box
	if addr, err := a.mcp.Start(9413); err == nil {
		a.mcpAddr = addr
		applog.Info("mcp listening at %s", addr)
	} else {
		applog.Error("mcp.start", err)
	}
}

func (a *App) shutdown(ctx context.Context) {
	a.mcp.Stop()
	_ = a.store.Close()
}

// ---------- fleet ----------

func (a *App) ListClusters() []model.Cluster { return a.store.ListClusters() }

// StorePath shows where the gateway keeps fleet config (SQLite + key.bin).
func (a *App) StorePath() string { return a.store.Path() }

// GetLogs returns the last n lines of the diagnostic log (no secrets inside).
func (a *App) GetLogs(n int) []string {
	if n <= 0 || n > 500 {
		n = 200
	}
	return applog.Tail(n)
}

// LogPath shows where the diagnostic log lives.
func (a *App) LogPath() string { return applog.Path() }

// ClearLogs truncates the diagnostic log.
func (a *App) ClearLogs() { _ = applog.Clear() }

// GetSettings returns app settings (appearance.* etc.).
func (a *App) GetSettings() map[string]string { return a.store.GetSettings() }

// SetSetting persists one setting, e.g. appearance.accent=indigo.
func (a *App) SetSetting(key, value string) {
	if key == "" {
		return
	}
	a.store.SetSetting(key, value)
}

func (a *App) SaveCluster(c model.Cluster) model.Cluster { return a.store.SaveCluster(c) }

func (a *App) DeleteCluster(id string) { a.store.DeleteCluster(id) }

func (a *App) ListSources() []model.Source { return a.store.ListSources() }

func (a *App) SaveSource(s model.Source) model.Source {
	if s.Status == "" {
		s.Status = model.StatusDraft
	}
	return a.store.SaveSource(s)
}

func (a *App) DeleteSource(id string) { a.store.DeleteSource(id) }

// ---------- onboarding ----------

type TestRequest struct {
	Source        model.Source `json:"source"`
	AdminUser     string       `json:"adminUser"`
	AdminPassword string       `json:"adminPassword"`
	AIUser        string       `json:"aiUser"`
	AIPassword    string       `json:"aiPassword"`
	AutoProvision bool         `json:"autoProvision"`
}

// TestConnection implements the onboarding invariant:
// SSH → detect → auth → SELECT → write-denied → ro-flag.
// When AutoProvision is set, it first creates the ai_readonly role with admin creds,
// then verifies AS the ai user, then forgets the admin password (never stored).
func (a *App) TestConnection(req TestRequest) []model.CheckResult {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	src := req.Source

	// SQLite files and Turso endpoints have no logins: verification is
	// open + proof (file integrity / guard self-test).
	if engines.FamilyOf(src.Engine) == engines.FamilySQLite {
		return hintChecks(lite.VerifyReadOnly(ctx, src))
	}
	if engines.FamilyOf(src.Engine) == engines.FamilyTurso {
		return hintChecks(turso.VerifyReadOnly(ctx, src))
	}
	if engines.FamilyOf(src.Engine) == engines.FamilyOther {
		return []model.CheckResult{{Key: "select", Label: "Engine support",
			Detail: fmt.Sprintf("%s is not bundled yet — pick a ready engine.", src.Engine)}}
	}

	aiUser := req.AIUser
	if aiUser == "" {
		aiUser = src.Username
	}
	if aiUser == "" {
		aiUser = "ai_readonly"
	}
	aiPass := req.AIPassword
	if aiPass == "" {
		aiPass = src.Password
	}

	if req.AutoProvision {
		if req.AdminUser == "" || req.AdminPassword == "" {
			return []model.CheckResult{{Key: "provision", Label: "Create dedicated read-only user", Detail: "admin user + password required for automatic setup"}}
		}
		if aiPass == "" {
			aiPass = provision.RandomPassword(28)
		}
		provChecks := a.provisionRole(ctx, src, req.AdminUser, req.AdminPassword, aiUser, aiPass)
		// if provisioning failed, return early
		ok := true
		for _, c := range provChecks {
			if !c.OK {
				ok = false
			}
		}
		if !ok {
			return hintChecks(provChecks)
		}
		// carry ai creds into verification (in-memory only)
		src.Username = aiUser
		src.Password = aiPass
		checks := dbops.VerifyReadOnly(ctx, src, aiUser, aiPass)
		return hintChecks(append(provChecks, checks...))
	}

	// Manual / existing-ai-user path: verify AS the supplied ai identity.
	if aiUser == "" || aiPass == "" {
		return []model.CheckResult{{Key: "auth", Label: "AI credentials", Detail: "supply the dedicated read-only username + password (or use automatic setup)"}}
	}
	src.Username = aiUser
	src.Password = aiPass
	return hintChecks(dbops.VerifyReadOnly(ctx, src, aiUser, aiPass))
}

type AdminTestRequest struct {
	Source        model.Source `json:"source"`
	AdminUser     string       `json:"adminUser"`
	AdminPassword string       `json:"adminPassword"`
}

// TestAdminConnection checks ONLY the temporary admin leg:
// SSH → server login → create-user privilege → target DB exists.
func (a *App) TestAdminConnection(req AdminTestRequest) []model.CheckResult {
	fam := engines.FamilyOf(req.Source.Engine)
	if fam != engines.FamilyPostgres && fam != engines.FamilyMySQL && fam != engines.FamilyMSSQL {
		return []model.CheckResult{{Key: "adm-auth", Label: "Admin credentials", Detail: "standalone admin login is a server-engine step — SQLite/Turso verify in one pass"}}
	}
	if req.AdminUser == "" || req.AdminPassword == "" {
		return []model.CheckResult{{Key: "adm-auth", Label: "Admin credentials", Detail: "enter the temporary admin user + password first"}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	switch fam {
	case engines.FamilyMySQL:
		return hintChecks(my.ProbeAdmin(ctx, req.Source, req.AdminUser, req.AdminPassword))
	case engines.FamilyMSSQL:
		return hintChecks(mssql.ProbeAdmin(ctx, req.Source, req.AdminUser, req.AdminPassword))
	default:
		return hintChecks(pg.ProbeAdmin(ctx, req.Source, req.AdminUser, req.AdminPassword))
	}
}

type ProvisionRequest struct {
	Source        model.Source `json:"source"`
	AdminUser     string       `json:"adminUser"`
	AdminPassword string       `json:"adminPassword"`
	AIUser        string       `json:"aiUser"`
	AIPassword    string       `json:"aiPassword"`
}

// ProvisionAIUser runs ONLY the create/reset step (create login + grants +
// proof) using temp admin creds. The admin password is never stored.
func (a *App) ProvisionAIUser(req ProvisionRequest) []model.CheckResult {
	fam := engines.FamilyOf(req.Source.Engine)
	if fam != engines.FamilyPostgres && fam != engines.FamilyMySQL && fam != engines.FamilyMSSQL {
		return []model.CheckResult{{Key: "provision", Label: "Create dedicated read-only user", Detail: "standalone provisioning is a server-engine step — SQLite/Turso need no users"}}
	}
	if req.AdminUser == "" || req.AdminPassword == "" {
		return []model.CheckResult{{Key: "provision", Label: "Create dedicated read-only user", Detail: "enter the temporary admin user + password first"}}
	}
	if req.AIUser == "" || req.AIPassword == "" {
		return []model.CheckResult{{Key: "provision", Label: "Create dedicated read-only user", Detail: "AI user + password are required"}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	switch fam {
	case engines.FamilyMySQL:
		return hintChecks(my.ProvisionRole(ctx, req.Source, req.AdminUser, req.AdminPassword, req.AIUser, req.AIPassword))
	case engines.FamilyMSSQL:
		return hintChecks(mssql.ProvisionRole(ctx, req.Source, req.AdminUser, req.AdminPassword, req.AIUser, req.AIPassword))
	default:
		return hintChecks(a.provisionAIRole(ctx, req.Source, req.AdminUser, req.AdminPassword, req.AIUser, req.AIPassword))
	}
}

// provisionRole dispatches role creation to the source's engine family.
func (a *App) provisionRole(ctx context.Context, src model.Source, adminUser, adminPass, aiUser, aiPass string) []model.CheckResult {
	switch engines.FamilyOf(src.Engine) {
	case engines.FamilyMySQL:
		return my.ProvisionRole(ctx, src, adminUser, adminPass, aiUser, aiPass)
	case engines.FamilyMSSQL:
		return mssql.ProvisionRole(ctx, src, adminUser, adminPass, aiUser, aiPass)
	default:
		return a.provisionAIRole(ctx, src, adminUser, adminPass, aiUser, aiPass)
	}
}

func (a *App) provisionAIRole(ctx context.Context, src model.Source, adminUser, adminPass, aiUser, aiPass string) []model.CheckResult {
	start := time.Now()
	pool, tun, err := pg.Connect(ctx, src, adminUser, adminPass, false)
	if err != nil {
		return []model.CheckResult{{Key: "provision", Label: "Create dedicated read-only user", Detail: err.Error(), Duration: time.Since(start).Milliseconds()}}
	}
	defer pool.Close()
	defer func() {
		if tun != nil {
			tun.Close()
		}
	}()
	sql := provision.GenerateReadOnlySQL(src.Database, aiUser, aiPass)
	// execute statement-by-statement is overkill: the script is idempotent DDL;
	// run key parts explicitly for clearer errors.
	stmts := []string{
		fmt.Sprintf(`CREATE ROLE "%s" WITH LOGIN PASSWORD '%s'`, escapeIdent(aiUser), escapeLit(aiPass)),
		fmt.Sprintf(`GRANT CONNECT ON DATABASE "%s" TO "%s"`, escapeIdent(src.Database), escapeIdent(aiUser)),
		fmt.Sprintf(`GRANT USAGE ON SCHEMA public TO "%s"`, escapeIdent(aiUser)),
		fmt.Sprintf(`GRANT SELECT ON ALL TABLES IN SCHEMA public TO "%s"`, escapeIdent(aiUser)),
		fmt.Sprintf(`ALTER DEFAULT PRIVILEGES FOR ROLE CURRENT_USER IN SCHEMA public GRANT SELECT ON TABLES TO "%s"`, escapeIdent(aiUser)),
		fmt.Sprintf(`ALTER ROLE "%s" SET default_transaction_read_only = on`, escapeIdent(aiUser)),
	}
	_ = sql
	created := true
	for i, st := range stmts {
		if _, err := pool.Exec(ctx, st); err != nil {
			// CREATE ROLE fails when the role already exists — then RESET
			// its password so re-running setup fixes a wrong/stale password.
			if i == 0 && isExistsErr(err) {
				created = false
				reset := fmt.Sprintf(`ALTER ROLE "%s" WITH LOGIN PASSWORD '%s'`, escapeIdent(aiUser), escapeLit(aiPass))
				if _, rerr := pool.Exec(ctx, reset); rerr != nil {
					return []model.CheckResult{{Key: "provision", Label: "Create dedicated read-only user", Detail: rerr.Error(), Duration: time.Since(start).Milliseconds()}}
				}
				continue
			}
			return []model.CheckResult{{Key: "provision", Label: "Create dedicated read-only user", Detail: err.Error(), Duration: time.Since(start).Milliseconds()}}
		}
	}
	// admin password is never stored — it only lives in this function scope.
	detail := fmt.Sprintf("role %q ready · admin credentials discarded", aiUser)
	if !created {
		detail = fmt.Sprintf("role %q already existed — password reset + grants re-applied · admin credentials discarded", aiUser)
	}
	prov := model.CheckResult{Key: "provision", Label: "Create dedicated read-only user", OK: true, Detail: detail, Duration: time.Since(start).Milliseconds()}
	// Prove the role is really inside postgres (visible to the admin session).
	var canlogin bool
	if err := pool.QueryRow(ctx, `SELECT rolcanlogin FROM pg_roles WHERE rolname=$1`, aiUser).Scan(&canlogin); err != nil {
		return []model.CheckResult{prov, {Key: "role-present", Label: "Role visible in pg_roles", Detail: "confirm failed: " + err.Error(), Duration: time.Since(start).Milliseconds()}}
	}
	return []model.CheckResult{prov, {Key: "role-present", Label: "Role visible in pg_roles", OK: true,
		Detail: fmt.Sprintf("%q exists · canlogin=%v · verified by admin session", aiUser, canlogin), Duration: time.Since(start).Milliseconds()}}
}

func escapeIdent(s string) string {
	out := ""
	for _, r := range s {
		if r == '"' {
			out += `""`
		} else {
			out += string(r)
		}
	}
	return out
}
func escapeLit(s string) string {
	out := ""
	for _, r := range s {
		if r == '\'' {
			out += `''`
		} else {
			out += string(r)
		}
	}
	return out
}
func isExistsErr(err error) bool {
	s := err.Error()
	return contains(s, "already exists") || contains(s, "42710")
}
func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(hay); i++ {
			if hay[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

// hintChecks appends next-step guidance to failed checks. PostgreSQL reports
// 28P01 for BOTH a wrong password and a missing role, so point at the fix.
func hintChecks(checks []model.CheckResult) []model.CheckResult {
	for i := range checks {
		c := &checks[i]
		if c.OK {
			continue
		}
		d := c.Detail
		switch {
		case strings.Contains(d, "28P01") || strings.Contains(d, "password authentication failed"):
			c.Detail += " ⟶ role missing or password wrong. Re-run automatic setup with temp admin creds — it creates the role, or resets its password if it exists."
		case strings.Contains(d, "connection refused"):
			c.Detail += " ⟶ nothing listening there. For SSH mode the DB must accept localhost:5432 ON the server."
		case strings.Contains(d, "no such host") || strings.Contains(d, "could not translate") || strings.Contains(d, "Name or service not known"):
			c.Detail += " ⟶ hostname does not resolve. Check the SSH host / DB host for typos."
		case strings.Contains(d, "timed out") || strings.Contains(d, "timeout") || strings.Contains(d, "i/o timeout"):
			c.Detail += " ⟶ firewall/security-group likely blocks it (SSH port 22 must be reachable)."
		case strings.Contains(d, "no ssh auth method"):
			c.Detail += " ⟶ pick Key file (Browse…), enter the SSH password, or start ssh-agent."
		case strings.Contains(d, "permission denied (publickey") || strings.Contains(d, "unable to authenticate"):
			c.Detail += " ⟶ server rejected the key/password. Verify SSH user + key, or switch auth mode."
		case strings.Contains(d, "role \"") && strings.Contains(d, "does not exist"):
			c.Detail += " ⟶ run automatic setup once with admin creds to create it."
		}
	}
	return checks
}
// EnableSource persists the source ONLY after proof of read-only.
func (a *App) EnableSource(src model.Source, checks []model.CheckResult) (model.Source, error) {
	for _, c := range checks {
		if (c.Key == "write" || c.Key == "select" || c.Key == "ro") && !c.OK {
			return src, fmt.Errorf("refusing to enable: %s failed — %s", c.Label, c.Detail)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	src.Status = model.StatusReady
	src.ReadOnlyVerified = true
	src.LastCheckAt = &now
	src.LastError = ""
	return a.store.SaveSource(src), nil
}

func (a *App) GenerateProvisionSQL(engine, dbName, aiUser, aiPass string) string {
	if model.Engine(engine) == model.EngineSQLite {
		return "-- SQLite needs no users or grants.\n-- Access control = file permissions + the gateway opening\n-- every database read-only (mode=ro, query_only=ON).\n-- Keep the .db file readable by you, and it stays AI-safe."
	}
	if model.Engine(engine) == model.EngineTurso {
		return "-- Turso needs no users or grants.\n-- Create a database token at https://turso.tech (or your sqld server),\n-- paste it as the auth token, and the gateway guards every query read-only."
	}
	switch engines.FamilyOf(model.Engine(engine)) {
	case engines.FamilyMySQL:
		return my.ProvisionSQL(dbName, aiUser, aiPass)
	case engines.FamilyMSSQL:
		return mssql.ProvisionSQL(dbName, aiUser, aiPass)
	case engines.FamilyOther:
		return "-- " + engine + " is not bundled yet: no provision script.\n-- Pick a ready engine to onboard."
	}
	if aiPass == "" {
		aiPass = provision.RandomPassword(28)
	}
	if aiUser == "" {
		aiUser = "ai_readonly"
	}
	return provision.GenerateReadOnlySQL(dbName, aiUser, aiPass)
}

func (a *App) GenerateManualScript(src model.Source, adminUser, aiUser string) string {
	if src.Engine == model.EngineSQLite {
		return "# SQLite needs no users — file permissions + read-only open enforce access."
	}
	if src.Engine == model.EngineTurso {
		return "# Turso needs no users — paste a database token as the auth token."
	}
	pass := provision.RandomPassword(28)
	if aiUser == "" {
		aiUser = "ai_readonly"
	}
	return provision.ManualScript(src.Host, coalescePort(src.Port), src.Database, adminUser, aiUser, pass)
}

func coalescePort(p int) int {
	if p == 0 {
		return 5432
	}
	return p
}

func (a *App) NewAIPassword() string { return provision.RandomPassword(28) }

func (a *App) TestSSH(src model.Source) string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = ctx
	keyPath, password := src.SSHKeyPath, src.SSHPassword
	if src.SSHAuth == "password" {
		keyPath = ""
	}
	if src.SSHAuth == "agent" {
		keyPath, password = "", ""
	}
	out, err := sshx.TestSSH(src.SSHHost, src.SSHPort, src.SSHUser, keyPath, src.SSHKeyPassphrase, password, 12*time.Second)
	if err != nil {
		return "SSH failed: " + err.Error()
	}
	return "SSH ok — " + out
}

// DefaultSSHKey returns the first existing private key under ~/.ssh
// (id_rsa first), else the id_rsa path as the default suggestion.
func (a *App) DefaultSSHKey() string {
	home, _ := os.UserHomeDir()
	for _, n := range []string{"id_rsa", "id_ed25519", "id_ecdsa"} {
		p := filepath.Join(home, ".ssh", n)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return filepath.Join(home, ".ssh", "id_rsa")
}

// PickSSHKey opens a native file dialog rooted at ~/.ssh.
func (a *App) PickSSHKey() string {
	home, _ := os.UserHomeDir()
	def := filepath.Join(home, ".ssh")
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		DefaultDirectory: def,
		Title:            "Select SSH private key",
	})
	if err != nil {
		return ""
	}
	return path
}

// PickSQLiteFile opens a native file dialog for .db/.sqlite files.
func (a *App) PickSQLiteFile() string {
	home, _ := os.UserHomeDir()
	path, err := runtime.OpenFileDialog(a.ctx, runtime.OpenDialogOptions{
		DefaultDirectory: home,
		Title:            "Select SQLite database file",
		Filters: []runtime.FileFilter{
			{DisplayName: "SQLite databases (*.db;*.sqlite;*.sqlite3)", Pattern: "*.db;*.sqlite;*.sqlite3"},
			{DisplayName: "All files (*.*)", Pattern: "*.*"},
		},
	})
	if err != nil {
		return ""
	}
	return path
}

// DiagnoseServer inspects the REMOTE side over SSH (read-only commands):
// is PG listening, is the role there, what does pg_hba allow.
// This distinguishes "wrong password" from "role missing" from "wrong server".
func (a *App) DiagnoseServer(src model.Source) []model.CheckResult {
	switch engines.FamilyOf(src.Engine) {
	case engines.FamilySQLite:
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return lite.Diagnose(ctx, src)
	case engines.FamilyTurso:
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return turso.Diagnose(ctx, src)
	case engines.FamilyMySQL:
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return my.Diagnose(ctx, src)
	case engines.FamilyMSSQL:
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		return mssql.Diagnose(ctx, src)
	}
	var out []model.CheckResult
	push := func(key, label string, fn func() (string, error)) {
		start := time.Now()
		detail, err := fn()
		out = append(out, model.CheckResult{
			Key: key, Label: label, OK: err == nil,
			Detail:   clip(detail, 400, err),
			Duration: time.Since(start).Milliseconds(),
		})
	}
	if src.Mode != model.ModeSSH {
		return []model.CheckResult{{Key: "mode", Label: "Server diagnosis", Detail: "direct mode — nothing remote to inspect"}}
	}
	keyPath, password := src.SSHKeyPath, src.SSHPassword
	if src.SSHAuth == "password" {
		keyPath = ""
	}
	if src.SSHAuth == "agent" {
		keyPath, password = "", ""
	}
	run := func(cmd string) (string, error) {
		return sshx.RunCommand(src.SSHHost, src.SSHPort, src.SSHUser, keyPath, src.SSHKeyPassphrase, password, cmd, 12*time.Second)
	}

	push("srv-listen", "PostgreSQL listening on server :5432", func() (string, error) {
		o, err := run(`(ss -ltn 2>/dev/null || netstat -ltn 2>/dev/null) | grep -E ':5432[[:space:]]' || echo NO_LISTENER_5432`)
		if err != nil {
			return o, err
		}
		if strings.Contains(o, "NO_LISTENER_5432") {
			return "nothing listening on 5432 — PG is down or on another port", fmt.Errorf("no listener on :5432")
		}
		return o, nil
	})
	push("srv-proc", "Postgres processes on server", func() (string, error) {
		o, err := run(`ps aux 2>/dev/null | grep '[p]ostgres' | head -4 || echo NO_POSTGRES_PROC`)
		if err != nil {
			return o, err
		}
		if strings.Contains(o, "NO_POSTGRES_PROC") {
			return "no postgres processes running", fmt.Errorf("postgres not running")
		}
		return o, nil
	})
	push("srv-role", fmt.Sprintf("Role %q exists in PG", roleName(src)), func() (string, error) {
		// peer auth as postgres OS user via passwordless sudo (best-effort).
		o, err := run(`sudo -n -u postgres psql -tAc "SELECT 'ROLE_FOUND:' || rolname || ' canlogin=' || rolcanlogin FROM pg_roles WHERE rolname = '` + escapeLit(roleName(src)) + `'"; echo SUDO_RC=$?`)
		if err != nil {
			return o, err
		}
		switch {
		case strings.Contains(o, "ROLE_FOUND:"):
			return o, nil
		case strings.Contains(o, "peer authentication failed"),
			strings.Contains(o, `role "postgres" does not exist`),
			strings.Contains(o, "could not connect"),
			strings.Contains(o, "sudo: a password is required"),
			strings.Contains(o, "user is not allowed"):
			short := strings.ReplaceAll(strings.TrimSpace(o), "\n", " ")
			if len(short) > 160 {
				short = short[:160] + "…"
			}
			return "cannot peek at pg_roles from here (" + short + ") — not an error, run automatic setup instead", nil
		default:
			return fmt.Sprintf("role %q NOT FOUND in pg_roles — create it via automatic setup", roleName(src)), fmt.Errorf("role missing")
		}
	})
	push("srv-hba", "pg_hba auth rules for 127.0.0.1", func() (string, error) {
		o, err := run(`(sudo -n cat /etc/postgresql/*/main/pg_hba.conf 2>/dev/null || cat /etc/postgresql/*/main/pg_hba.conf 2>/dev/null || cat /var/lib/pgsql/data/pg_hba.conf 2>/dev/null) | grep -v '^#' | grep -v '^[[:space:]]*$' | head -20`)
		if err != nil || strings.TrimSpace(o) == "" {
			return "pg_hba.conf not readable (needs root) — ask the server admin to confirm a 'host … 127.0.0.1/32 scram-sha-256' line", nil
		}
		return o, nil
	})
	return out
}

func roleName(src model.Source) string {
	if src.Username != "" {
		return src.Username
	}
	return "ai_readonly"
}

func clip(s string, n int, err error) string {
	if err != nil {
		if s == "" {
			return err.Error()
		}
		if len(s) > n {
			s = s[:n] + "…"
		}
		return s + " ⟶ " + err.Error()
	}
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// TestSSHByID runs the SSH leg only, for a saved source.
func (a *App) TestSSHByID(id string) string {
	src, ok := a.store.GetSource(id)
	if !ok {
		return "unknown source"
	}
	if src.Mode != model.ModeSSH {
		return "direct connection — no SSH hop to test"
	}
	return a.TestSSH(src)
}

// ReverifySource re-runs the read-only proof using STORED ai credentials
// and persists the outcome (ready+verified, or error with reason).
func (a *App) ReverifySource(id string) []model.CheckResult {
	src, ok := a.store.GetSource(id)
	if !ok {
		return []model.CheckResult{{Key: "lookup", Label: "Source", Detail: "not found"}}
	}
	if src.Username == "" || src.Password == "" {
		return []model.CheckResult{{Key: "auth", Label: "AI credentials", Detail: "no stored ai credentials — run Add Database setup first"}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	checks := dbops.VerifyReadOnly(ctx, src, src.Username, src.Password)
	checks = hintChecks(checks)
	now := time.Now().UTC().Format(time.RFC3339)
	verified := true
	for _, c := range checks {
		if (c.Key == "write" || c.Key == "select" || c.Key == "ro") && !c.OK {
			verified = false
		}
	}
	if verified {
		src.Status = model.StatusReady
		src.ReadOnlyVerified = true
		src.LastError = ""
	} else {
		src.Status = model.StatusError
		src.ReadOnlyVerified = false
		for _, c := range checks {
			if !c.OK {
				src.LastError = c.Label + ": " + c.Detail
				break
			}
		}
	}
	src.LastCheckAt = &now
	a.store.SaveSource(src)
	return checks
}

// ---------- app write mode (Beekeeper-style edits, app UI only) ----------

// WriteMode reports the master switch (Settings → General). Default off.
func (a *App) WriteMode() bool {
	return a.store.GetSettings()["app.allowWrites"] == "on"
}

// SetWriteMode flips the master switch. Everything edit-related checks it.
func (a *App) SetWriteMode(on bool) {
	v := "off"
	if on {
		v = "on"
	}
	a.store.SetSetting("app.allowWrites", v)
}

// HasWriteUser reports whether a privileged login is saved for the source.
// SQLite needs none (file permissions rule); Turso reuses its token.
func (a *App) HasWriteUser(sourceID string) bool {
	src, ok := a.store.GetSource(sourceID)
	if !ok {
		return false
	}
	switch engines.FamilyOf(src.Engine) {
	case engines.FamilySQLite, engines.FamilyTurso:
		return true
	default:
		_, _, ok := a.store.GetWriteUser(sourceID)
		return ok
	}
}

// WriteUserName returns the saved privileged login name ("" when none).
// The password is never returned — it only enters a live connection.
func (a *App) WriteUserName(sourceID string) string {
	src, ok := a.store.GetSource(sourceID)
	if !ok {
		return ""
	}
	switch engines.FamilyOf(src.Engine) {
	case engines.FamilySQLite:
		return "(file)"
	case engines.FamilyTurso:
		return "(token)"
	default:
		return a.store.WriteUserName(sourceID)
	}
}

// SaveWriteUser stores the privileged login (AES-GCM). Empty password
// clears it. Rejected for file/token engines (nothing to store).
func (a *App) SaveWriteUser(sourceID, username, password string) string {
	src, ok := a.store.GetSource(sourceID)
	if !ok {
		return "unknown source"
	}
	switch engines.FamilyOf(src.Engine) {
	case engines.FamilySQLite, engines.FamilyTurso:
		return "this engine needs no write login"
	}
	if username == "" || password == "" {
		return "username + password are required"
	}
	a.store.SaveWriteUser(sourceID, username, password)
	return ""
}

// DeleteWriteUser removes the privileged login for a source.
func (a *App) DeleteWriteUser(sourceID string) {
	a.store.DeleteWriteUser(sourceID)
}

// ExecWrite runs one app-confirmed INSERT/UPDATE/DELETE. Gates, in order:
// master switch → source → credentials → SQL validation (+ full-table
// confirmation). MCP has no path to this method.
func (a *App) ExecWrite(sourceID, sqlText string, confirmedFullTable bool) (model.WriteResult, error) {
	if !a.WriteMode() {
		return model.WriteResult{}, fmt.Errorf("write mode is off — enable it in Settings → General first")
	}
	src, ok := a.store.GetSource(sourceID)
	if !ok {
		return model.WriteResult{}, fmt.Errorf("unknown source")
	}
	var user, pass string
	switch engines.FamilyOf(src.Engine) {
	case engines.FamilySQLite:
		// file permissions + confirmation are the control
	case engines.FamilyTurso:
		if src.Password == "" {
			return model.WriteResult{}, fmt.Errorf("no auth token saved for this source")
		}
		user, pass = src.Username, src.Password
	default:
		var ok bool
		user, pass, ok = a.store.GetWriteUser(sourceID)
		if !ok {
			return model.WriteResult{}, fmt.Errorf("no write login saved — set it from Browse → Edit rows first")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Now()
	n, err := dbops.ExecWrite(ctx, src, user, pass, sqlText, confirmedFullTable)
	if err != nil {
		applog.Error("execwrite "+src.Name, err)
		return model.WriteResult{}, err
	}
	return model.WriteResult{RowsAffected: n, DurationMs: time.Since(start).Milliseconds()}, nil
}

func (a *App) GetSchema(sourceID string) (model.SchemaInfo, error) {
	src, ok := a.store.GetSource(sourceID)
	if !ok {
		return model.SchemaInfo{}, fmt.Errorf("unknown source")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sch, err := dbops.Schema(ctx, src)
	if err != nil {
		applog.Error("schema "+src.Name, err)
		return sch, err
	}
	return sch, nil
}

// GetTableColumns loads one table's columns on demand (fast Browse).
func (a *App) GetTableColumns(sourceID, schema, table string) ([]model.ColumnInfo, error) {
	src, ok := a.store.GetSource(sourceID)
	if !ok {
		return nil, fmt.Errorf("unknown source")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cols, err := dbops.Columns(ctx, src, schema, table)
	if err != nil {
		applog.Error(fmt.Sprintf("columns %s %s.%s", src.Name, schema, table), err)
		return nil, err
	}
	return cols, nil
}

func (a *App) RunQuery(sourceID, sql string) (model.QueryResult, error) {
	src, ok := a.store.GetSource(sourceID)
	if !ok {
		return model.QueryResult{}, fmt.Errorf("unknown source")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := dbops.Query(ctx, src, sql, 200)
	if err != nil {
		applog.Error("query "+src.Name, err)
		return res, err
	}
	return res, nil
}

func (a *App) SampleTable(sourceID, schema, table string) (model.QueryResult, error) {
	ident := `"` + schema + `"."` + table + `"`
	return a.RunQuery(sourceID, "SELECT * FROM "+ident)
}

// PreviewTable is the Browse fast path: paged + filtered rows, long values
// truncated, binary guarded — safe even for huge tables.
func (a *App) PreviewTable(sourceID, schema, table string, limit, offset int, filters []pg.Filter, raw string) (model.QueryResult, error) {
	src, ok := a.store.GetSource(sourceID)
	if !ok {
		return model.QueryResult{}, fmt.Errorf("unknown source")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := dbops.Preview(ctx, src, schema, table, limit, offset, filters, raw)
	if err != nil {
		applog.Error(fmt.Sprintf("preview %s %s.%s", src.Name, schema, table), err)
		return res, err
	}
	return res, nil
}

func (a *App) GetDoctor(sourceID string) ([]model.DoctorFinding, error) {
	src, ok := a.store.GetSource(sourceID)
	if !ok {
		return nil, fmt.Errorf("unknown source")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := dbops.DoctorFindings(ctx, src)
	if err != nil {
		applog.Error("doctor "+src.Name, err)
		return out, err
	}
	return out, nil
}

// ---------- fleet for AI ----------

func (a *App) FleetOverview() model.FleetOverview {
	clusters := a.store.ListClusters()
	cname := map[string]string{}
	for _, c := range clusters {
		cname[c.ID] = c.Name
	}
	var pubs []model.SourcePublic
	for _, s := range a.store.ListSources() {
		pubs = append(pubs, model.SourcePublic{
			Name: s.Name, Cluster: cname[s.ClusterID], Engine: string(s.Engine),
			Database: s.Database, Status: string(s.Status), ReadOnly: s.ReadOnlyVerified,
		})
	}
	return model.FleetOverview{Clusters: clusters, Sources: pubs}
}

// ---------- MCP ----------

func (a *App) MCPStatus() map[string]any {
	return map[string]any{"running": a.mcp.Running(), "url": a.mcpAddr}
}

func (a *App) StartMCP() string {
	if addr, err := a.mcp.Start(9413); err == nil {
		a.mcpAddr = addr
	}
	return a.mcpAddr
}

// MCPConfig returns the copy-paste JSON for opencode / Claude / Cursor.
func (a *App) MCPConfig() string {
	url := a.mcpAddr
	if url == "" {
		url = "http://127.0.0.1:9413"
	}
	return fmt.Sprintf(`{
  "mcpServers": {
    "readgate": {
      "url": "%s/mcp",
      "description": "ReadGate — read-only fleet gateway. Tools: fleet_overview, list_sources, schema, columns, query, explain, sample, doctor."
    }
  }
}`, url)
}

// exePath resolves this binary's path for MCP stdio configs.
func exePath() string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = `E:\laragon\www\go\ReadGate\build\bin\ReadGate.exe`
	}
	return exe
}

// OpencodeConfig returns the copy-paste JSON for opencode.json — LOCAL stdio
// mode (goals parity): opencode launches `ReadGate.exe mcp` itself, no app
// window or HTTP port needed. (The Claude "mcpServers" shape is silently
// ignored by opencode, which surfaces as "Failed to get tools".)
func (a *App) OpencodeConfig() string {
	esc := strings.ReplaceAll(exePath(), `\`, `\\`)
	return fmt.Sprintf(`{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "readgate": {
      "type": "local",
      "command": ["%s", "mcp"],
      "enabled": true
    }
  }
}`, esc)
}

// ClaudeConfig returns the copy-paste stdio JSON for Claude Desktop, Cursor,
// Windsurf, Cline and any client using the {"mcpServers": ...} shape:
// they launch `ReadGate.exe mcp` themselves, so no app window is needed.
func (a *App) ClaudeConfig() string {
	esc := strings.ReplaceAll(exePath(), `\`, `\\`)
	return fmt.Sprintf(`{
  "mcpServers": {
    "readgate": {
      "command": "%s",
      "args": ["mcp"],
      "description": "ReadGate — read-only fleet gateway. Tools: fleet_overview, list_sources, schema, columns, query, explain, sample, doctor."
    }
  }
}`, esc)
}

// TestMCP performs the exact handshake an AI client does — initialize,
// notifications/initialized, tools/list, tools/call — and reports each leg.
// If this is green and opencode still fails, the problem is the client
// config (wrong format) or the app not running, not the protocol.
func (a *App) TestMCP() string {
	base := a.mcpAddr
	if base == "" {
		base = "http://127.0.0.1:9413"
	}
	call := func(body string) (int, string) {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, "POST", base+"/mcp", strings.NewReader(body))
		if err != nil {
			return 0, err.Error()
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, err.Error()
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		s := string(b)
		if len(s) > 300 {
			s = s[:300] + "…"
		}
		return resp.StatusCode, s
	}
	var sb strings.Builder
	sb.WriteString("== stdio (`ReadGate.exe mcp`, goals parity) ==\n")
	sb.WriteString(testMCPStdio())
	sb.WriteString("\n== http (127.0.0.1:9413/mcp, kept for other clients) ==\n")
	st, b := call(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"readgate-selftest","version":"0.1.0"}}}`)
	fmt.Fprintf(&sb, "initialize → %d %s\n", st, b)
	st, _ = call(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	fmt.Fprintf(&sb, "notifications/initialized → %d (want 202, empty)\n", st)
	st, b = call(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`)
	nTools := strings.Count(b, `"name":`) - 0
	fmt.Fprintf(&sb, "tools/list → %d (%d tools)\n", st, nTools)
	st, b = call(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"fleet_overview","arguments":{}}}`)
	fmt.Fprintf(&sb, "tools/call fleet_overview → %d %s", st, b)
	return sb.String()
}

func (a *App) MCPToolsPreview() string {
	return `fleet_overview · list_sources · list_clusters · schema(source) · columns(source, table) · query(source, sql) · explain(source, sql) · sample(source, table) · doctor(source) · table_stats(source[, table]) · indexes(source, table) · relationships(source[, table]) · search_tables(source, pattern) · slow_queries(source)`
}

// testMCPStdio spawns this same binary as `mcp` (like opencode does) and runs
// the handshake over piped stdin/stdout.
func testMCPStdio() string {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return "stdio → cannot resolve own executable: " + fmt.Sprint(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "mcp")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "stdio → stdin pipe: " + err.Error()
	}
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf
	if err := cmd.Start(); err != nil {
		return "stdio → start: " + err.Error()
	}
	lines := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"readgate-selftest","version":"0.1.0"}}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"fleet_overview","arguments":{}}}`,
	}
	for _, l := range lines {
		if _, err := io.WriteString(stdin, l+"\n"); err != nil {
			_ = cmd.Process.Kill()
			return "stdio → write: " + err.Error()
		}
	}
	_ = stdin.Close()
	_ = cmd.Wait()
	out := strings.TrimSpace(outBuf.String())
	if out == "" {
		return "stdio → no output (subprocess died silently)"
	}
	var sb strings.Builder
	n := 0
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || !strings.HasPrefix(l, "{") {
			continue
		}
		n++
		if len(l) > 220 {
			l = l[:220] + "…"
		}
		fmt.Fprintf(&sb, "stdio msg %d → %s\n", n, l)
	}
	if n == 0 {
		return "stdio → no JSON-RPC replies. raw: " + clip(out, 220, nil)
	}
	return strings.TrimRight(sb.String(), "\n")
}
