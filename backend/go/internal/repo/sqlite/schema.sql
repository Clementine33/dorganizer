-- SQLite schema V2 for onsei organizer
-- Entries table for tracked files
CREATE TABLE IF NOT EXISTS entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    path TEXT NOT NULL UNIQUE,
    path_posix TEXT NOT NULL,
    file_size INTEGER,
    bitrate INTEGER,
    duration_ms INTEGER,
    format TEXT,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    updated_at TEXT DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_entries_path_posix ON entries(path_posix);
CREATE INDEX IF NOT EXISTS idx_entries_path ON entries(path);

-- Staging table for scan operations
CREATE TABLE IF NOT EXISTS entries_staging (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    path TEXT NOT NULL,
    path_posix TEXT NOT NULL,
    operation TEXT NOT NULL,
    status TEXT DEFAULT 'pending',
    scan_id TEXT,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX IF NOT EXISTS idx_entries_staging_scan_id ON entries_staging(scan_id);
CREATE INDEX IF NOT EXISTS idx_entries_staging_status ON entries_staging(status);
