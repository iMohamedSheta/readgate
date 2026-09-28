// Package dbops routes every data-plane operation to the implementation
// matching the source's engine family. app.go and the MCP server both call
// through here, so a new engine lights up browse, query, doctor, and all
// AI tools at once. Engines without a bundled driver fail loudly —
// a source can never silently run against the wrong dialect.
package dbops

import (
	"context"
	"fmt"

	"readgate/internal/engines"
	"readgate/internal/lite"
	"readgate/internal/model"
	"readgate/internal/mssql"
	"readgate/internal/my"
	"readgate/internal/pg"
	"readgate/internal/turso"
)

// withPort fills the conventional port when the source leaves it at 0.
func withPort(src model.Source) model.Source {
	if src.Port == 0 {
		src.Port = engines.DefaultPort(src.Engine)
	}
	return src
}

func notBundled(op string, src model.Source) error {
	m, ok := engines.ByID(src.Engine)
	name := string(src.Engine)
	why := "no driver bundled"
	if ok {
		name = m.Label
		if m.Note != "" {
			why = m.Note
		}
	}
	return fmt.Errorf("%s: %s is not bundled yet (%s)", op, name, why)
}

func route(src model.Source) (engines.Family, error) {
	fam := engines.FamilyOf(src.Engine)
	if fam == engines.FamilyOther {
		return fam, notBundled("operation", src)
	}
	return fam, nil
}

func Schema(ctx context.Context, src model.Source) (model.SchemaInfo, error) {
	fam, err := route(src)
	if err != nil {
		return model.SchemaInfo{}, err
	}
	src = withPort(src)
	switch fam {
	case engines.FamilySQLite:
		return lite.Schema(ctx, src)
	case engines.FamilyMySQL:
		return my.Schema(ctx, src)
	case engines.FamilyMSSQL:
		return mssql.Schema(ctx, src)
	case engines.FamilyTurso:
		return turso.Schema(ctx, src)
	default:
		return pg.Schema(ctx, src)
	}
}

func Columns(ctx context.Context, src model.Source, schema, table string) ([]model.ColumnInfo, error) {
	fam, err := route(src)
	if err != nil {
		return nil, err
	}
	src = withPort(src)
	switch fam {
	case engines.FamilySQLite:
		return lite.Columns(ctx, src, schema, table)
	case engines.FamilyMySQL:
		return my.Columns(ctx, src, schema, table)
	case engines.FamilyMSSQL:
		return mssql.Columns(ctx, src, schema, table)
	case engines.FamilyTurso:
		return turso.Columns(ctx, src, schema, table)
	default:
		return pg.Columns(ctx, src, schema, table)
	}
}

func Query(ctx context.Context, src model.Source, sqlText string, limit int) (model.QueryResult, error) {
	fam, err := route(src)
	if err != nil {
		return model.QueryResult{}, err
	}
	src = withPort(src)
	switch fam {
	case engines.FamilySQLite:
		return lite.Query(ctx, src, sqlText, limit)
	case engines.FamilyMySQL:
		return my.Query(ctx, src, sqlText, limit)
	case engines.FamilyMSSQL:
		return mssql.Query(ctx, src, sqlText, limit)
	case engines.FamilyTurso:
		return turso.Query(ctx, src, sqlText, limit)
	default:
		return pg.Query(ctx, src, sqlText, limit)
	}
}

func Preview(ctx context.Context, src model.Source, schema, table string, limit, offset int, filters []model.Filter, raw string) (model.QueryResult, error) {
	fam, err := route(src)
	if err != nil {
		return model.QueryResult{}, err
	}
	src = withPort(src)
	switch fam {
	case engines.FamilySQLite:
		return lite.Preview(ctx, src, schema, table, limit, offset, filters, raw)
	case engines.FamilyMySQL:
		return my.Preview(ctx, src, schema, table, limit, offset, filters, raw)
	case engines.FamilyMSSQL:
		return mssql.Preview(ctx, src, schema, table, limit, offset, filters, raw)
	case engines.FamilyTurso:
		return turso.Preview(ctx, src, schema, table, limit, offset, filters, raw)
	default:
		return pg.Preview(ctx, src, schema, table, limit, offset, filters, raw)
	}
}

func Explain(ctx context.Context, src model.Source, sqlText string) (string, error) {
	fam, err := route(src)
	if err != nil {
		return "", err
	}
	src = withPort(src)
	switch fam {
	case engines.FamilySQLite:
		return lite.Explain(ctx, src, sqlText)
	case engines.FamilyMySQL:
		return my.Explain(ctx, src, sqlText)
	case engines.FamilyMSSQL:
		return mssql.Explain(ctx, src, sqlText)
	case engines.FamilyTurso:
		return turso.Explain(ctx, src, sqlText)
	default:
		return pg.Explain(ctx, src, sqlText)
	}
}

func DoctorFindings(ctx context.Context, src model.Source) ([]model.DoctorFinding, error) {
	fam, err := route(src)
	if err != nil {
		return nil, err
	}
	src = withPort(src)
	switch fam {
	case engines.FamilySQLite:
		return lite.DoctorFindings(ctx, src)
	case engines.FamilyMySQL:
		return my.DoctorFindings(ctx, src)
	case engines.FamilyMSSQL:
		return mssql.DoctorFindings(ctx, src)
	case engines.FamilyTurso:
		return turso.DoctorFindings(ctx, src)
	default:
		return pg.DoctorFindings(ctx, src)
	}
}

// VerifyReadOnly runs the read-only proof. The returned checks always use
// the select/write/ro keys so the enable invariant holds for every engine.
func VerifyReadOnly(ctx context.Context, src model.Source, dbUser, dbPass string) []model.CheckResult {
	fam, err := route(src)
	if err != nil {
		return []model.CheckResult{{Key: "select", Label: "Engine support", Detail: err.Error()}}
	}
	src = withPort(src)
	switch fam {
	case engines.FamilySQLite:
		return lite.VerifyReadOnly(ctx, src)
	case engines.FamilyMySQL:
		return my.VerifyReadOnly(ctx, src, dbUser, dbPass)
	case engines.FamilyMSSQL:
		return mssql.VerifyReadOnly(ctx, src, dbUser, dbPass)
	case engines.FamilyTurso:
		return turso.VerifyReadOnly(ctx, src)
	default:
		return pg.VerifyReadOnly(ctx, src, dbUser, dbPass)
	}
}

func TableStats(ctx context.Context, src model.Source, schema, table string, limit int) ([]map[string]any, error) {
	fam, err := route(src)
	if err != nil {
		return nil, err
	}
	src = withPort(src)
	switch fam {
	case engines.FamilySQLite:
		return lite.TableStats(ctx, src, schema, table, limit)
	case engines.FamilyMySQL:
		return my.TableStats(ctx, src, schema, table, limit)
	case engines.FamilyMSSQL:
		return mssql.TableStats(ctx, src, schema, table, limit)
	case engines.FamilyTurso:
		return turso.TableStats(ctx, src, schema, table, limit)
	default:
		return pg.TableStats(ctx, src, schema, table, limit)
	}
}

func Indexes(ctx context.Context, src model.Source, schema, table string) ([]map[string]any, error) {
	fam, err := route(src)
	if err != nil {
		return nil, err
	}
	src = withPort(src)
	switch fam {
	case engines.FamilySQLite:
		return lite.Indexes(ctx, src, schema, table)
	case engines.FamilyMySQL:
		return my.Indexes(ctx, src, schema, table)
	case engines.FamilyMSSQL:
		return mssql.Indexes(ctx, src, schema, table)
	case engines.FamilyTurso:
		return turso.Indexes(ctx, src, schema, table)
	default:
		return pg.Indexes(ctx, src, schema, table)
	}
}

func Relationships(ctx context.Context, src model.Source, schema, table string, limit int) ([]map[string]any, error) {
	fam, err := route(src)
	if err != nil {
		return nil, err
	}
	src = withPort(src)
	switch fam {
	case engines.FamilySQLite:
		return lite.Relationships(ctx, src, schema, table, limit)
	case engines.FamilyMySQL:
		return my.Relationships(ctx, src, schema, table, limit)
	case engines.FamilyMSSQL:
		return mssql.Relationships(ctx, src, schema, table, limit)
	case engines.FamilyTurso:
		return turso.Relationships(ctx, src, schema, table, limit)
	default:
		return pg.Relationships(ctx, src, schema, table, limit)
	}
}

func SearchTables(ctx context.Context, src model.Source, pattern string, limit int) ([]map[string]any, error) {
	fam, err := route(src)
	if err != nil {
		return nil, err
	}
	src = withPort(src)
	switch fam {
	case engines.FamilySQLite:
		return lite.SearchTables(ctx, src, pattern, limit)
	case engines.FamilyMySQL:
		return my.SearchTables(ctx, src, pattern, limit)
	case engines.FamilyMSSQL:
		return mssql.SearchTables(ctx, src, pattern, limit)
	case engines.FamilyTurso:
		return turso.SearchTables(ctx, src, pattern, limit)
	default:
		return pg.SearchTables(ctx, src, pattern, limit)
	}
}

func SlowQueries(ctx context.Context, src model.Source, limit int) (map[string]any, error) {	fam, err := route(src)
	if err != nil {
		return nil, err
	}
	src = withPort(src)
	switch fam {
	case engines.FamilySQLite:
		return lite.SlowQueries(ctx, src, limit)
	case engines.FamilyMySQL:
		return my.SlowQueries(ctx, src, limit)
	case engines.FamilyMSSQL:
		return mssql.SlowQueries(ctx, src, limit)
	case engines.FamilyTurso:
		return turso.SlowQueries(ctx, src, limit)
	default:
		return pg.SlowQueries(ctx, src, limit)
	}
}

// ExecWrite runs one app-confirmed row modification as user/pass.
// The caller (app.go) enforces the master switch + credentials; MCP has
// no path to this function at all.
func ExecWrite(ctx context.Context, src model.Source, dbUser, dbPass, sqlText string, allowFullTable bool) (int64, error) {
	fam, err := route(src)
	if err != nil {
		return 0, err
	}
	src = withPort(src)
	switch fam {
	case engines.FamilySQLite:
		return lite.ExecWrite(ctx, src, dbUser, dbPass, sqlText, allowFullTable)
	case engines.FamilyMySQL:
		return my.ExecWrite(ctx, src, dbUser, dbPass, sqlText, allowFullTable)
	case engines.FamilyMSSQL:
		return mssql.ExecWrite(ctx, src, dbUser, dbPass, sqlText, allowFullTable)
	case engines.FamilyTurso:
		return turso.ExecWrite(ctx, src, dbUser, dbPass, sqlText, allowFullTable)
	default:
		return pg.ExecWrite(ctx, src, dbUser, dbPass, sqlText, allowFullTable)
	}
}
