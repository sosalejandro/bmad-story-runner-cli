package sqlite

import (
	"database/sql"
	"strings"
	"time"
)

// Helpers shared by adapters for null-column scanning + writing.

func nullString(p *string) sql.NullString {
	if p == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *p, Valid: true}
}

func nullInt(p *int) sql.NullInt64 {
	if p == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*p), Valid: true}
}

func nullTime(p *time.Time) sql.NullTime {
	if p == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *p, Valid: true}
}

func nullFloat(p *float64) sql.NullFloat64 {
	if p == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *p, Valid: true}
}

func ptrString(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	v := ns.String
	return &v
}

func ptrInt(ni sql.NullInt64) *int {
	if !ni.Valid {
		return nil
	}
	v := int(ni.Int64)
	return &v
}

func ptrTime(nt sql.NullTime) *time.Time {
	if !nt.Valid {
		return nil
	}
	v := nt.Time
	return &v
}

func ptrFloat(nf sql.NullFloat64) *float64 {
	if !nf.Valid {
		return nil
	}
	v := nf.Float64
	return &v
}

// joinIDs / splitIDs serialize a []string into / out of the container_ids TEXT
// column. Comma separator is fine here — container IDs from `docker compose
// ps -q` are hex hashes and never contain commas.
func joinIDs(ids []string) string {
	return strings.Join(ids, ",")
}

func splitIDs(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}
