package store

// Write users: privileged logins (postgres / root / sa …) saved ONLY to
// enable confirmed row edits inside the app. Encrypted with key.bin like
// every other secret, and NEVER exposed to the AI: fleet payloads, MCP
// tools, logs, and diagnostics only ever see names.

func (s *Store) migrateWriteUsers() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS write_users(
		source_id TEXT PRIMARY KEY,
		username TEXT NOT NULL DEFAULT '',
		password_enc TEXT NOT NULL DEFAULT '',
		updated_at TEXT NOT NULL DEFAULT ''
	)`)
	return err
}

// SaveWriteUser stores (or replaces) the privileged login for a source.
// Empty password clears the entry.
func (s *Store) SaveWriteUser(sourceID, username, password string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if username == "" || password == "" {
		_, _ = s.db.Exec(`DELETE FROM write_users WHERE source_id = ?`, sourceID)
		return
	}
	_, _ = s.db.Exec(`INSERT INTO write_users(source_id, username, password_enc, updated_at)
		VALUES(?, ?, ?, ?) ON CONFLICT(source_id) DO UPDATE SET
		username = excluded.username, password_enc = excluded.password_enc, updated_at = excluded.updated_at`,
		sourceID, username, s.encrypt(password), Now())
}

// GetWriteUser returns the decrypted login, or ok=false when none is saved.
func (s *Store) GetWriteUser(sourceID string) (username, password string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var u, penc string
	if err := s.db.QueryRow(`SELECT username, password_enc FROM write_users WHERE source_id = ?`, sourceID).Scan(&u, &penc); err != nil {
		return "", "", false
	}
	p := s.decrypt(penc)
	if u == "" || p == "" {
		return "", "", false
	}
	return u, p, true
}

// WriteUserName returns the saved login name ("" when none). The password
// never leaves the store except into a live write connection.
func (s *Store) WriteUserName(sourceID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var u string
	if err := s.db.QueryRow(`SELECT username FROM write_users WHERE source_id = ?`, sourceID).Scan(&u); err != nil {
		return ""
	}
	return u
}

// DeleteWriteUser removes the privileged login for a source.
func (s *Store) DeleteWriteUser(sourceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.db.Exec(`DELETE FROM write_users WHERE source_id = ?`, sourceID)
}

// DeleteSource cascades its write user (sources table keeps no FK).
func (s *Store) deleteWriteUserLocked(sourceID string) {
	_, _ = s.db.Exec(`DELETE FROM write_users WHERE source_id = ?`, sourceID)
}
