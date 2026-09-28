// Package engines is the fleet's engine registry: every database Beekeeper
// Studio supports, with ReadGate's support status for each.
//
// Status levels:
//   ready — fully implemented (proof, browse, query, MCP).
//   beta  — works over a compatible wire protocol, best effort.
//   soon  — relational and pluggable, needs a driver bundled (no network at
//           build time right now, so the driver can't be vendored yet).
//   planned — needs vendor client libs or a non-SQL API (bigger project).
package engines

import "readgate/internal/model"

// Family groups wire-compatible engines behind one implementation.
type Family string

const (
	FamilyPostgres Family = "postgres" // pgx wire: postgres, cockroachdb, redshift
	FamilyMySQL    Family = "mysql"    // mysql wire: mysql, mariadb, tidb
	FamilySQLite   Family = "sqlite"   // embedded file, no server
	FamilyTurso    Family = "turso"    // libsql HTTP (Turso / sqld), sqlite dialect
	FamilyMSSQL    Family = "mssql"    // TDS: sql server
	FamilyOther    Family = "other"    // not bundled yet
)

type Status string

const (
	StatusReady   Status = "ready"
	StatusBeta    Status = "beta"
	StatusSoon    Status = "soon"
	StatusPlanned Status = "planned"
)

type Meta struct {
	ID          model.Engine `json:"id"`
	Label       string       `json:"label"`
	DefaultPort int          `json:"defaultPort"` // 0 = no port (file)
	Family      Family       `json:"family"`
	Status      Status       `json:"status"`
	Note        string       `json:"note"`
	AdminUser   string       `json:"adminUser"`
	AIUser      string       `json:"aiUser"`
}

// Registry mirrors Beekeeper Studio's supported-databases list, plus Turso.
var Registry = []Meta{
	{ID: model.EnginePostgres, Label: "PostgreSQL", DefaultPort: 5432, Family: FamilyPostgres, Status: StatusReady, AdminUser: "postgres", AIUser: "ai_readonly"},
	{ID: model.EngineMySQL, Label: "MySQL", DefaultPort: 3306, Family: FamilyMySQL, Status: StatusReady, AdminUser: "root", AIUser: "ai_readonly"},
	{ID: model.EngineMariaDB, Label: "MariaDB", DefaultPort: 3306, Family: FamilyMySQL, Status: StatusReady, AdminUser: "root", AIUser: "ai_readonly"},
	{ID: model.EngineSQLite, Label: "SQLite", DefaultPort: 0, Family: FamilySQLite, Status: StatusReady, Note: "Local file · no server, no login — pick a .db file", AIUser: "ro"},
	{ID: model.EngineTurso, Label: "Turso", DefaultPort: 443, Family: FamilyTurso, Status: StatusReady, Note: "libsql URL + token · gateway-enforced read-only", AIUser: "token"},
	{ID: model.EngineSQLServer, Label: "SQL Server", DefaultPort: 1433, Family: FamilyMSSQL, Status: StatusReady, AdminUser: "sa", AIUser: "ai_readonly"},
	{ID: model.EngineCockroach, Label: "CockroachDB", DefaultPort: 26257, Family: FamilyPostgres, Status: StatusBeta, Note: "Postgres wire — best effort, auto-provision may differ", AdminUser: "root", AIUser: "ai_readonly"},
	{ID: model.EngineTiDB, Label: "TiDB", DefaultPort: 4000, Family: FamilyMySQL, Status: StatusReady, AdminUser: "root", AIUser: "ai_readonly"},
	{ID: model.EngineRedshift, Label: "Amazon Redshift", DefaultPort: 5439, Family: FamilyPostgres, Status: StatusBeta, Note: "Postgres wire — best effort, some catalogs differ", AdminUser: "admin", AIUser: "ai_readonly"},
	{ID: model.EngineClickHouse, Label: "ClickHouse", DefaultPort: 8123, Family: FamilyOther, Status: StatusSoon, Note: "Needs clickhouse-go driver + dialect"},
	{ID: model.EngineOracle, Label: "Oracle", DefaultPort: 1521, Family: FamilyOther, Status: StatusPlanned, Note: "Needs Oracle client libraries + PL/SQL dialect"},
}

func ByID(id model.Engine) (Meta, bool) {
	for _, m := range Registry {
		if m.ID == id {
			return m, true
		}
	}
	return Meta{}, false
}

// FamilyOf maps an engine to its implementation family.
// Unknown engines fall back to postgres semantics (strictest guard wins).
func FamilyOf(id model.Engine) Family {
	if m, ok := ByID(id); ok {
		return m.Family
	}
	if id == "" {
		return FamilyPostgres
	}
	return FamilyOther
}

// DefaultPort returns the conventional port (0 = file, no port).
func DefaultPort(id model.Engine) int {
	if m, ok := ByID(id); ok {
		return m.DefaultPort
	}
	return 5432
}

// Selectable reports whether the wizard may create this engine today.
func Selectable(id model.Engine) bool {
	m, ok := ByID(id)
	if !ok {
		return false
	}
	return m.Status == StatusReady || m.Status == StatusBeta
}
