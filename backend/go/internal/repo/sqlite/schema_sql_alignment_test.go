package sqlite

import (
	"os"
	"strings"
	"testing"
)

// normalizeSQL collapses whitespace and lowercases SQL for robust matching.
func normalizeSQL(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// extractEntriesTableDDL returns the substring from "create table if not exists entries"
// through the first ");" that closes the statement. Returns "" if not found.
func extractEntriesTableDDL(content string) string {
	lower := strings.ToLower(content)
	start := strings.Index(lower, "create table if not exists entries")
	if start == -1 {
		return ""
	}
	end := strings.Index(lower[start:], ");")
	if end == -1 {
		return ""
	}
	return content[start : start+end+2]
}

func TestSchemaSQL_ContainsRuntimeEntriesColumnsAndIndex(t *testing.T) {
	data, err := os.ReadFile("schema.sql")
	if err != nil {
		t.Fatalf("failed to read schema.sql: %v", err)
	}
	content := string(data)

	entriesDDL := extractEntriesTableDDL(content)
	if entriesDDL == "" {
		t.Fatal("could not locate entries CREATE TABLE block in schema.sql")
	}
	normDDL := normalizeSQL(entriesDDL)
	normFull := normalizeSQL(content)

	// Column assertions — scoped to entries DDL only
	columnChecks := []struct {
		desc string
		frag string
	}{
		// PK
		{"path PK", "path text primary key"},
		// Runtime model columns
		{"root_path", "root_path text not null default ''"},
		{"parent_path", "parent_path text not null default ''"},
		{"name", "name text not null default ''"},
		{"is_dir", "is_dir integer not null default 0"},
		{"size", "size integer not null default 0"},
		{"mtime", "mtime integer not null default 0"},
		{"scan_id", "scan_id text not null default ''"},
		{"content_rev", "content_rev integer not null default 1"},
		{"dirty_flag", "dirty_flag integer not null default 0"},
		{"is_error", "is_error integer not null default 0"},
		{"error_reason", "error_reason text"},
		// Legacy columns
		{"path_posix legacy", "path_posix text not null default ''"},
		{"file_size legacy", "file_size integer"},
		{"duration_ms legacy", "duration_ms integer"},
		{"format legacy", "format text"},
	}

	for _, req := range columnChecks {
		if !strings.Contains(normDDL, req.frag) {
			t.Errorf("schema.sql entries table missing %s: expected fragment %q", req.desc, req.frag)
		}
	}

	// Index assertions — file-level is fine
	indexChecks := []struct {
		desc string
		frag string
	}{
		{"idx_entries_root_path", "create index if not exists idx_entries_root_path on entries(root_path)"},
		{"idx_entries_parent_path", "create index if not exists idx_entries_parent_path on entries(parent_path)"},
		{"idx_entries_path_posix", "create index if not exists idx_entries_path_posix on entries(path_posix)"},
		{"idx_entries_path", "create index if not exists idx_entries_path on entries(path)"},
		{"idx_entries_root_dir_path", "create index if not exists idx_entries_root_dir_path on entries(root_path, is_dir, path)"},
	}

	for _, req := range indexChecks {
		if !strings.Contains(normFull, req.frag) {
			t.Errorf("schema.sql missing index %s: expected fragment %q", req.desc, req.frag)
		}
	}
}
