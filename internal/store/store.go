// Package store persists the fleet in SQLite with AES-GCM secrets.
package store

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"readgate/internal/model"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// Store persists clusters + sources in SQLite (pure Go, no CGO).
// DB file: ~/.readgate/readgate.db (WAL). Password columns are AES-GCM
// encrypted with ~/.readgate/key.bin (0600). Legacy store.json is
// imported once, then renamed to store.json.bak.
type Store struct {
	mu      sync.Mutex
	dir     string
	dbPath  string
	keyFile string
	key     []byte
	db      *sql.DB
}

func AppDir() string {
	if v := os.Getenv("READGATE_HOME"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".readgate")
}

func Now() string { return time.Now().UTC().Format(time.RFC3339) }

func New() (*Store, error) {
	dir := AppDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	s := &Store{dir: dir, dbPath: filepath.Join(dir, "readgate.db"), keyFile: filepath.Join(dir, "key.bin")}
	if err := s.loadKey(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", s.dbPath)
	if err != nil {
		return nil, err
	}
	s.db = db
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		return nil, err
	}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	_ = os.Chmod(s.dbPath, 0o600)
	if err := s.importLegacyJSON(); err != nil {
		return nil, err
	}
	// Fresh installs start EMPTY (no demo data). Existing DBs get a
	// one-time cleanup of the old demo rows.
	if err := s.cleanupFakeSeeds(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Path() string { return s.dbPath }

func (s *Store) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

const schema = `
CREATE TABLE IF NOT EXISTS clusters(
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL DEFAULT '',
	description TEXT NOT NULL DEFAULT '',
	color TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS sources(
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL DEFAULT '',
	cluster_id TEXT NOT NULL DEFAULT '',
	engine TEXT NOT NULL DEFAULT 'postgres',
	mode TEXT NOT NULL DEFAULT 'direct',
	host TEXT NOT NULL DEFAULT '',
	port INTEGER NOT NULL DEFAULT 5432,
	database TEXT NOT NULL DEFAULT '',
	username TEXT NOT NULL DEFAULT '',
	password_enc TEXT NOT NULL DEFAULT '',
	ssh_host TEXT NOT NULL DEFAULT '',
	ssh_port INTEGER NOT NULL DEFAULT 22,
	ssh_user TEXT NOT NULL DEFAULT '',
	ssh_auth TEXT NOT NULL DEFAULT 'key',
	ssh_key_path TEXT NOT NULL DEFAULT '',
	ssh_pass_enc TEXT NOT NULL DEFAULT '',
	ssh_password_enc TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'draft',
	read_only_verified INTEGER NOT NULL DEFAULT 0,
	last_check_at TEXT,
	last_error TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_sources_cluster ON sources(cluster_id);
CREATE TABLE IF NOT EXISTS settings(
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL DEFAULT ''
);
`

func (s *Store) migrate() error {
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	if err := s.migrateWriteUsers(); err != nil {
		return err
	}
	// additive upgrades for DBs created by older versions (ignore dup errors)
	for _, col := range []string{
		`ALTER TABLE sources ADD COLUMN ssh_auth TEXT NOT NULL DEFAULT 'key'`,
		`ALTER TABLE sources ADD COLUMN ssh_password_enc TEXT NOT NULL DEFAULT ''`,
	} {
		_, _ = s.db.Exec(col)
	}
	return nil
}

// ---- key + column encryption (unchanged format: gcm:/plain:) ----

func (s *Store) loadKey() error {
	if b, err := os.ReadFile(s.keyFile); err == nil && len(b) == 32 {
		s.key = b
		return nil
	}
	k := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, k); err != nil {
		return err
	}
	if err := os.WriteFile(s.keyFile, k, 0o600); err != nil {
		return err
	}
	s.key = k
	return nil
}

func (s *Store) encrypt(plain string) string {
	if plain == "" {
		return ""
	}
	block, err := aes.NewCipher(s.key)
	if err != nil {
		return "plain:" + plain
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "plain:" + plain
	}
	nonce := make([]byte, gcm.NonceSize())
	io.ReadFull(rand.Reader, nonce)
	ct := gcm.Seal(nonce, nonce, []byte(plain), nil)
	return "gcm:" + base64.StdEncoding.EncodeToString(ct)
}

func (s *Store) decrypt(enc string) string {
	if enc == "" {
		return ""
	}
	if len(enc) > 4 && enc[:4] == "gcm:" {
		raw, err := base64.StdEncoding.DecodeString(enc[4:])
		if err != nil {
			return ""
		}
		block, err := aes.NewCipher(s.key)
		if err != nil {
			return ""
		}
		gcm, err := cipher.NewGCM(block)
		if err != nil {
			return ""
		}
		nonce := raw[:gcm.NonceSize()]
		ct := raw[gcm.NonceSize():]
		pt, err := gcm.Open(nil, nonce, ct, nil)
		if err != nil {
			return ""
		}
		return string(pt)
	}
	if len(enc) > 6 && enc[:6] == "plain:" {
		return enc[6:]
	}
	return enc // legacy raw
}

// ---- legacy JSON import (one time) ----

func (s *Store) importLegacyJSON() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	legacy := filepath.Join(s.dir, "store.json")
	b, err := os.ReadFile(legacy)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sources`).Scan(&n); err != nil {
		return err
	}
	var m int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM clusters`).Scan(&m); err != nil {
		return err
	}
	if n+m > 0 {
		// DB already populated — archive the json without importing.
		_ = os.Rename(legacy, legacy+".bak")
		return nil
	}
	var raw struct {
		Clusters []model.Cluster `json:"clusters"`
		Sources  []model.Source  `json:"sources"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	for _, c := range raw.Clusters {
		if c.ID == "" {
			c.ID = uuid.NewString()
		}
		if c.CreatedAt == "" {
			c.CreatedAt = Now()
		}
		s.saveClusterLocked(c)
	}
	for _, src := range raw.Sources {
		if src.ID == "" {
			src.ID = uuid.NewString()
		}
		if src.CreatedAt == "" {
			src.CreatedAt = Now()
		}
		// passwords in legacy file are already encrypted strings —
		// round-trip through decrypt so saveSourceLocked re-encrypts.
		src.Password = s.decrypt(src.Password)
		src.SSHKeyPassphrase = s.decrypt(src.SSHKeyPassphrase)
		if src.Port == 0 {
			src.Port = 5432
		}
		if src.SSHPort == 0 && src.Mode == model.ModeSSH {
			src.SSHPort = 22
		}
		s.saveSourceLocked(src)
	}
	_ = os.Rename(legacy, legacy+".bak")
	return nil
}

// cleanupFakeSeeds removes the old demo rows exactly once.
// 203.0.113.x is TEST-NET-3 documentation space — never a real server.
// Demo clusters are removed only when left source-less.
func (s *Store) cleanupFakeSeeds() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.db.Exec(`DELETE FROM sources WHERE ssh_host LIKE '203.0.113.%'`)
	_, _ = s.db.Exec(`DELETE FROM clusters
		WHERE description IN ('Customer-facing fleet','Warehouses & pipelines')
		AND NOT EXISTS (SELECT 1 FROM sources WHERE sources.cluster_id = clusters.id)`)
	return nil
}

// ---- clusters ----

func (s *Store) ListClusters() []model.Cluster {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id,name,description,color,created_at FROM clusters ORDER BY name`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []model.Cluster
	for rows.Next() {
		var c model.Cluster
		if err := rows.Scan(&c.ID, &c.Name, &c.Description, &c.Color, &c.CreatedAt); err == nil {
			out = append(out, c)
		}
	}
	return out
}

func (s *Store) SaveCluster(c model.Cluster) model.Cluster {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c.ID == "" {
		c.ID = uuid.NewString()
		c.CreatedAt = Now()
	}
	s.saveClusterLocked(c)
	return c
}

func (s *Store) saveClusterLocked(c model.Cluster) {
	_, _ = s.db.Exec(`INSERT INTO clusters(id,name,description,color,created_at)
		VALUES(?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name,description=excluded.description,color=excluded.color`,
		c.ID, c.Name, c.Description, c.Color, c.CreatedAt)
}

func (s *Store) DeleteCluster(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.db.Exec(`DELETE FROM clusters WHERE id=?`, id)
	_, _ = s.db.Exec(`UPDATE sources SET cluster_id='' WHERE cluster_id=?`, id)
}

// ---- sources ----

func scanSource(rows *sql.Rows) (model.Source, error) {
	var x model.Source
	var ro int
	var lastCheck sql.NullString
	var sshPassEnc string
	err := rows.Scan(&x.ID, &x.Name, &x.ClusterID, &x.Engine, &x.Mode,
		&x.Host, &x.Port, &x.Database, &x.Username, &x.Password,
		&x.SSHHost, &x.SSHPort, &x.SSHUser, &x.SSHAuth, &x.SSHKeyPath, &x.SSHKeyPassphrase, &sshPassEnc,
		&x.Status, &ro, &lastCheck, &x.LastError, &x.CreatedAt)
	if err != nil {
		return x, err
	}
	x.ReadOnlyVerified = ro == 1
	if lastCheck.Valid {
		v := lastCheck.String
		x.LastCheckAt = &v
	}
	x.SSHPassword = sshPassEnc
	return x, err
}

const sourceCols = `id,name,cluster_id,engine,mode,host,port,database,username,password_enc,ssh_host,ssh_port,ssh_user,ssh_auth,ssh_key_path,ssh_pass_enc,ssh_password_enc,status,read_only_verified,last_check_at,last_error,created_at`

func (s *Store) ListSources() []model.Source {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT `+sourceCols+` FROM sources ORDER BY name`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []model.Source
	for rows.Next() {
		x, err := scanSource(rows)
		if err != nil {
			continue
		}
		x.Password = s.decrypt(x.Password)
		x.SSHKeyPassphrase = s.decrypt(x.SSHKeyPassphrase)
		x.SSHPassword = s.decrypt(x.SSHPassword)
		out = append(out, x)
	}
	return out
}

func (s *Store) GetSource(id string) (model.Source, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT `+sourceCols+` FROM sources WHERE id=?`, id)
	if err != nil {
		return model.Source{}, false
	}
	defer rows.Close()
	if !rows.Next() {
		return model.Source{}, false
	}
	x, err := scanSource(rows)
	if err != nil {
		return model.Source{}, false
	}
	x.Password = s.decrypt(x.Password)
	x.SSHKeyPassphrase = s.decrypt(x.SSHKeyPassphrase)
	x.SSHPassword = s.decrypt(x.SSHPassword)
	return x, true
}

func (s *Store) SaveSource(src model.Source) model.Source {
	s.mu.Lock()
	defer s.mu.Unlock()
	if src.ID == "" {
		src.ID = uuid.NewString()
		src.CreatedAt = Now()
	}
	if src.Port == 0 {
		src.Port = 5432
	}
	if src.SSHPort == 0 && src.Mode == model.ModeSSH {
		src.SSHPort = 22
	}
	if src.Status == "" {
		src.Status = model.StatusDraft
	}
	if src.SSHAuth == "" {
		src.SSHAuth = "key"
	}
	s.saveSourceLocked(src)
	return src
}

func (s *Store) saveSourceLocked(src model.Source) {
	ro := 0
	if src.ReadOnlyVerified {
		ro = 1
	}
	var lastCheck any
	if src.LastCheckAt != nil {
		lastCheck = *src.LastCheckAt
	}
	_, _ = s.db.Exec(`INSERT INTO sources(`+sourceCols+`)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
		name=excluded.name,cluster_id=excluded.cluster_id,engine=excluded.engine,mode=excluded.mode,
		host=excluded.host,port=excluded.port,database=excluded.database,username=excluded.username,
		password_enc=excluded.password_enc,ssh_host=excluded.ssh_host,ssh_port=excluded.ssh_port,
		ssh_user=excluded.ssh_user,ssh_auth=excluded.ssh_auth,ssh_key_path=excluded.ssh_key_path,
		ssh_pass_enc=excluded.ssh_pass_enc,ssh_password_enc=excluded.ssh_password_enc,
		status=excluded.status,read_only_verified=excluded.read_only_verified,
		last_check_at=excluded.last_check_at,last_error=excluded.last_error`,
		src.ID, src.Name, src.ClusterID, string(src.Engine), string(src.Mode),
		src.Host, src.Port, src.Database, src.Username, s.encrypt(src.Password),
		src.SSHHost, src.SSHPort, src.SSHUser, src.SSHAuth, src.SSHKeyPath,
		s.encrypt(src.SSHKeyPassphrase), s.encrypt(src.SSHPassword),
		string(src.Status), ro, lastCheck, src.LastError, src.CreatedAt)
}

func (s *Store) DeleteSource(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.db.Exec(`DELETE FROM sources WHERE id=?`, id)
	s.deleteWriteUserLocked(id)
}

// ---- settings (appearance etc.) ----

func (s *Store) GetSettings() map[string]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]string{}
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err == nil {
			out[k] = v
		}
	}
	return out
}

func (s *Store) SetSetting(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.db.Exec(`INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
}
