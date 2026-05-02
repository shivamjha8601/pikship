package dbtx

// Role identifies which Postgres role a pool's connections SET ROLE to.
//
// Three roles, three pools — see LLD §02-infrastructure/01-database-access:
//
//   RoleApp      — pikshipp_app, RLS-enforced, default for handlers + workers.
//   RoleReports  — pikshipp_reports, BYPASSRLS read-mostly, used by reports/.
//   RoleAdmin    — pikshipp_admin, BYPASSRLS audit-elevated writes, used by admin/.
type Role int

const (
	RoleApp Role = iota
	RoleReports
	RoleAdmin
)

// String returns the Postgres role name (matches the role created in
// migrations/0001_init_schema.up.sql).
func (r Role) String() string {
	switch r {
	case RoleApp:
		return "pikshipp_app"
	case RoleReports:
		return "pikshipp_reports"
	case RoleAdmin:
		return "pikshipp_admin"
	default:
		return "unknown"
	}
}
