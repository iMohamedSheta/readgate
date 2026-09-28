// Package model holds the shared fleet types. Timestamps are RFC3339
// strings, not time.Time: Wails cannot bind time.Time and JSON "" fails
// to unmarshal into it, which made creates fail silently.
package model
// Cluster groups fleet sources (Production, Analytics, …).
type Cluster struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Color       string `json:"color"`
	CreatedAt   string `json:"createdAt"`
}

type Engine string

const (
	EnginePostgres   Engine = "postgres"
	EngineCockroach  Engine = "cockroachdb"
	EngineRedshift   Engine = "redshift"
	EngineMySQL      Engine = "mysql"
	EngineMariaDB    Engine = "mariadb"
	EngineTiDB       Engine = "tidb"
	EngineSQLite     Engine = "sqlite"
	EngineTurso      Engine = "turso"
	EngineSQLServer  Engine = "sqlserver"
	EngineClickHouse Engine = "clickhouse"
	EngineOracle     Engine = "oracle"
)

type ConnectionMode string

const (
	ModeDirect ConnectionMode = "direct"
	ModeSSH    ConnectionMode = "ssh"
	// ModeFile is for embedded files (SQLite): Database holds the file path,
	// there is no host/port, no SSH, and no server login.
	ModeFile ConnectionMode = "file"
)

type SourceStatus string

const (
	StatusDraft    SourceStatus = "draft"
	StatusVerifying SourceStatus = "verifying"
	StatusReady    SourceStatus = "ready"
	StatusError    SourceStatus = "error"
)

// Source is a single database behind the gateway.
// The AI only ever sees Name + Cluster. Credentials never leave the gateway.
type Source struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	ClusterID        string         `json:"clusterId"`
	Engine           Engine         `json:"engine"`
	Mode             ConnectionMode `json:"mode"`
	Host             string         `json:"host"`
	Port             int            `json:"port"`
	Database         string         `json:"database"`
	Username         string         `json:"username"` // the dedicated ai_readonly user
	Password         string         `json:"password"` // ai user password (encrypted at rest)
	SSHHost          string         `json:"sshHost"`
	SSHPort          int            `json:"sshPort"`
	SSHUser          string         `json:"sshUser"`
	SSHAuth          string         `json:"sshAuth"` // key | password | agent
	SSHKeyPath       string         `json:"sshKeyPath"`
	SSHKeyPassphrase string         `json:"sshKeyPassphrase,omitempty"`
	SSHPassword      string         `json:"sshPassword,omitempty"`
	Status           SourceStatus   `json:"status"`
	ReadOnlyVerified bool           `json:"readOnlyVerified"`
	LastCheckAt      *string        `json:"lastCheckAt,omitempty"`
	LastError        string         `json:"lastError,omitempty"`
	CreatedAt        string         `json:"createdAt"`
}

type CheckResult struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	OK       bool   `json:"ok"`
	Detail   string `json:"detail"`
	Duration int64  `json:"durationMs"`
}

type ColumnInfo struct {
	Name     string `json:"name"`
	Type     string `json:"type"`
	Nullable bool   `json:"nullable"`
	Default  string `json:"default,omitempty"`
}

type TableInfo struct {
	Schema      string       `json:"schema"`
	Name        string       `json:"name"`
	RowEstimate int64        `json:"rowEstimate"`
	Columns     []ColumnInfo `json:"columns,omitempty"`
}

type SchemaInfo struct {
	SourceName string      `json:"sourceName"`
	Tables     []TableInfo `json:"tables"`
}

type QueryResult struct {
	Columns []string        `json:"columns"`
	Rows    [][]interface{} `json:"rows"`
	RowCount int            `json:"rowCount"`
	// TotalRows is the exact filtered total (-1 = unknown, use estimate).
	TotalRows int64 `json:"totalRows"`
	DurationMs int64        `json:"durationMs"`
	Truncated bool          `json:"truncated"`
}

// WriteResult reports an app-confirmed row modification (never via MCP).
type WriteResult struct {
	RowsAffected int64 `json:"rowsAffected"`
	DurationMs   int64 `json:"durationMs"`
}

type DoctorFinding struct {
	Severity string `json:"severity"` // info | warn | critical
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Remedy   string `json:"remedy,omitempty"`
	Source   string `json:"source"`
}

// Filter is one Beekeeper-style builder condition.
// Op: eq ne gt gte lt lte contains starts ends like in null notnull.
// Logic joins with the PREVIOUS condition: AND | OR.
type Filter struct {
	Column string `json:"column"`
	Op     string `json:"op"`
	Value  string `json:"value"`
	Logic  string `json:"logic"`
}

type FleetOverview struct {
	Clusters []Cluster      `json:"clusters"`
	Sources  []SourcePublic `json:"sources"`
}

// SourcePublic is what the AI is allowed to see.
type SourcePublic struct {
	Name      string `json:"name"`
	Cluster   string `json:"cluster"`
	Engine    string `json:"engine"`
	Database  string `json:"database"`
	Status    string `json:"status"`
	ReadOnly  bool   `json:"readOnly"`
	TableCount int   `json:"tableCount,omitempty"`
}
