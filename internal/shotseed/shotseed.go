// Package shotseed builds a safe, photogenic demo profile for screenshots.
//
// The release workflow runs the built binary once as
// `ReadGate --shot-seed` with READGATE_HOME pointed at a fresh temp dir,
// then launches the app against that profile and screenshots the window.
// Everything here is synthetic (example.com, fictional people) so release
// screenshots never leak a real host, database, or customer row.
//
// Layout photographed: Fleet tab (the default) with 2 clusters + 3 SQLite
// sources, all verified read-only, each backed by a real local .db file
// with pleasant sample tables so Browse/Query also work live.
package shotseed

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"readgate/internal/model"
	"readgate/internal/store"

	_ "modernc.org/sqlite"
)

// Run creates demo files + fleet rows in the current READGATE_HOME and exits.
// Idempotent: re-running wipes nothing, it upserts by fixed IDs.
func Run() error {
	dir := store.AppDir()
	demoDir := filepath.Join(dir, "shot-demo")
	if err := os.MkdirAll(demoDir, 0o700); err != nil {
		return err
	}

	shopPath := filepath.Join(demoDir, "shop.db")
	analyticsPath := filepath.Join(demoDir, "analytics.db")
	billingPath := filepath.Join(demoDir, "billing.db")

	if err := makeShop(shopPath); err != nil {
		return fmt.Errorf("shop db: %w", err)
	}
	if err := makeAnalytics(analyticsPath); err != nil {
		return fmt.Errorf("analytics db: %w", err)
	}
	if err := makeBilling(billingPath); err != nil {
		return fmt.Errorf("billing db: %w", err)
	}

	st, err := store.New()
	if err != nil {
		return err
	}
	defer st.Close()

	now := time.Now().UTC().Format(time.RFC3339)

	prod := st.SaveCluster(model.Cluster{
		ID: "shot-prod", Name: "Production", Description: "Customer-facing fleet",
		Color: "#10b981", CreatedAt: now,
	})
	analytics := st.SaveCluster(model.Cluster{
		ID: "shot-analytics", Name: "Analytics", Description: "Warehouses & pipelines",
		Color: "#6366f1", CreatedAt: now,
	})

	seed := []model.Source{
		{
			ID: "shot-shop", Name: "Shop-Prod", ClusterID: prod.ID,
			Engine: model.EngineSQLite, Mode: model.ModeFile, Database: shopPath,
			Status: model.StatusReady, ReadOnlyVerified: true,
			LastCheckAt: &now, CreatedAt: now,
		},
		{
			ID: "shot-billing", Name: "Billing", ClusterID: prod.ID,
			Engine: model.EngineSQLite, Mode: model.ModeFile, Database: billingPath,
			Status: model.StatusReady, ReadOnlyVerified: true,
			LastCheckAt: &now, CreatedAt: now,
		},
		{
			ID: "shot-warehouse", Name: "Warehouse", ClusterID: analytics.ID,
			Engine: model.EngineSQLite, Mode: model.ModeFile, Database: analyticsPath,
			Status: model.StatusReady, ReadOnlyVerified: true,
			LastCheckAt: &now, CreatedAt: now,
		},
	}
	for _, s := range seed {
		st.SaveSource(s)
	}
	return nil
}

func execAll(db *sql.DB, stmts []string) error {
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("%w\n-- %s", err, s)
		}
	}
	return nil
}

func makeShop(path string) error {
	_ = os.Remove(path)
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		return err
	}
	defer db.Close()
	schema := []string{
		`CREATE TABLE customers(id INTEGER PRIMARY KEY, name TEXT, email TEXT, city TEXT, created_at TEXT)`,
		`CREATE TABLE products(id INTEGER PRIMARY KEY, name TEXT, price_cents INTEGER, category TEXT)`,
		`CREATE TABLE orders(id INTEGER PRIMARY KEY, customer_id INTEGER REFERENCES customers(id), status TEXT, total_cents INTEGER, created_at TEXT)`,
	}
	if err := execAll(db, schema); err != nil {
		return err
	}
	customers := [][4]string{
		{"Ava Stone", "ava@example.com", "Berlin", "2026-01-04"},
		{"Liam Carter", "liam@example.com", "London", "2026-01-11"},
		{"Mia Haddad", "mia@example.com", "Cairo", "2026-02-02"},
		{"Noah Silva", "noah@example.com", "Lisbon", "2026-02-19"},
		{"Emma Novak", "emma@example.com", "Prague", "2026-03-07"},
		{"Lucas Meyer", "lucas@example.com", "Munich", "2026-03-21"},
		{"Sofia Rossi", "sofia@example.com", "Milan", "2026-04-09"},
		{"Ethan Wright", "ethan@example.com", "Austin", "2026-05-16"},
	}
	for i, c := range customers {
		if _, err := db.Exec(`INSERT INTO customers(id,name,email,city,created_at) VALUES(?,?,?,?,?)`,
			i+1, c[0], c[1], c[2], c[3]); err != nil {
			return err
		}
	}
	products := [][3]any{
		{"Starter plan", 900, "plans"},
		{"Pro plan", 4900, "plans"},
		{"Extra seats (10)", 1900, "addons"},
		{"Audit export", 2900, "addons"},
	}
	for i, p := range products {
		if _, err := db.Exec(`INSERT INTO products(id,name,price_cents,category) VALUES(?,?,?,?)`,
			i+1, p[0], p[1], p[2]); err != nil {
			return err
		}
	}
	orders := [][4]any{
		{1, "paid", 4900, "2026-06-01"}, {2, "paid", 5800, "2026-06-03"},
		{3, "refunded", 900, "2026-06-05"}, {1, "paid", 1900, "2026-06-09"},
		{5, "pending", 4900, "2026-06-12"}, {6, "paid", 7800, "2026-06-15"},
		{4, "paid", 2900, "2026-06-18"}, {7, "paid", 4900, "2026-06-21"},
		{8, "failed", 900, "2026-06-24"}, {2, "paid", 2900, "2026-06-27"},
		{5, "paid", 6800, "2026-07-02"}, {6, "paid", 4900, "2026-07-06"},
	}
	for i, o := range orders {
		if _, err := db.Exec(`INSERT INTO orders(id,customer_id,status,total_cents,created_at) VALUES(?,?,?,?,?)`,
			i+1, o[0], o[1], o[2], o[3]); err != nil {
			return err
		}
	}
	return nil
}

func makeAnalytics(path string) error {
	_ = os.Remove(path)
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		return err
	}
	defer db.Close()
	schema := []string{
		`CREATE TABLE events(id INTEGER PRIMARY KEY, type TEXT, user_id INTEGER, created_at TEXT)`,
		`CREATE TABLE daily_stats(date TEXT PRIMARY KEY, signups INTEGER, revenue_cents INTEGER)`,
	}
	if err := execAll(db, schema); err != nil {
		return err
	}
	types := []string{"signup", "login", "checkout", "invite", "export"}
	for i := 1; i <= 40; i++ {
		t := types[i%len(types)]
		day := fmt.Sprintf("2026-07-%02d", 1+(i%27))
		if _, err := db.Exec(`INSERT INTO events(id,type,user_id,created_at) VALUES(?,?,?,?)`,
			i, t, 1+(i%8), day); err != nil {
			return err
		}
	}
	for d := 1; d <= 14; d++ {
		date := fmt.Sprintf("2026-07-%02d", d)
		if _, err := db.Exec(`INSERT INTO daily_stats(date,signups,revenue_cents) VALUES(?,?,?)`,
			date, 4+(d%9), 12000+(d*2300)%60000); err != nil {
			return err
		}
	}
	return nil
}

func makeBilling(path string) error {
	_ = os.Remove(path)
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		return err
	}
	defer db.Close()
	schema := []string{
		`CREATE TABLE invoices(id INTEGER PRIMARY KEY, customer TEXT, amount_cents INTEGER, status TEXT, due_date TEXT)`,
		`CREATE TABLE payments(id INTEGER PRIMARY KEY, invoice_id INTEGER REFERENCES invoices(id), amount_cents INTEGER, paid_at TEXT)`,
	}
	if err := execAll(db, schema); err != nil {
		return err
	}
	invoices := [][4]any{
		{"Acme Ltd", 49000, "paid", "2026-06-30"},
		{"Globex", 12900, "paid", "2026-07-05"},
		{"Initech", 7800, "pending", "2026-07-15"},
		{"Umbrella", 24000, "overdue", "2026-06-20"},
		{"Hooli", 9900, "paid", "2026-07-01"},
		{"Stark Labs", 56000, "pending", "2026-07-20"},
	}
	for i, inv := range invoices {
		if _, err := db.Exec(`INSERT INTO invoices(id,customer,amount_cents,status,due_date) VALUES(?,?,?,?,?)`,
			i+1, inv[0], inv[1], inv[2], inv[3]); err != nil {
			return err
		}
	}
	payments := [][3]any{
		{1, 49000, "2026-06-28"}, {2, 12900, "2026-07-03"},
		{4, 12000, "2026-06-25"}, {5, 9900, "2026-06-29"},
	}
	for i, p := range payments {
		if _, err := db.Exec(`INSERT INTO payments(id,invoice_id,amount_cents,paid_at) VALUES(?,?,?,?)`,
			i+1, p[0], p[1], p[2]); err != nil {
			return err
		}
	}
	return nil
}
