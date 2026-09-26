-- Uploaded drives and their data (PR 4). A drive row belongs to one agent;
-- copies of one physical drive from different agents are linked through
-- agent_physical_drives, never merged.

CREATE TABLE agent_physical_drives (       -- one per real drive, across all of a user's agents
  id              BIGSERIAL PRIMARY KEY,
  user_id         BIGINT NOT NULL REFERENCES agent_users(id),
  fs_uuid         TEXT NOT NULL,             -- normalised: uppercase, no dashes
  fs_type         TEXT,
  fs_uuid_source  TEXT,                      -- 'linux' | 'macos'; part of the match for FAT/exFAT/NTFS
  hw_serial       TEXT,                      -- NULL if never reported
  clone_of        BIGINT REFERENCES agent_physical_drives(id),
  first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX agent_physical_drives_match ON agent_physical_drives (user_id, fs_uuid);

CREATE TABLE agent_drives (
  id                BIGSERIAL PRIMARY KEY,
  agent_id          UUID NOT NULL REFERENCES agent_agents(id),
  drive_id          TEXT NOT NULL,             -- the agent's label, e.g. "seagate2"
  physical_drive_id BIGINT REFERENCES agent_physical_drives(id),  -- NULL if not matched
  fs_uuid           TEXT,                      -- identity as last reported by the agent
  fs_type           TEXT,
  fs_uuid_source    TEXT,
  hw_serial         TEXT,
  stream_id         UUID NOT NULL,
  acked_version     BIGINT NOT NULL DEFAULT 0, -- watermark: end of the acked range starting at 0
  drive_root        TEXT NOT NULL,
  backup_root       TEXT NOT NULL DEFAULT '',
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  last_synced_at    TIMESTAMPTZ,
  UNIQUE (agent_id, drive_id)
);
CREATE INDEX agent_drives_physical ON agent_drives (physical_drive_id);

CREATE TABLE agent_files (
  drive_pk        BIGINT NOT NULL REFERENCES agent_drives(id) ON DELETE CASCADE,
  path_key        BYTEA NOT NULL,            -- sha256(raw path bytes)
  relative_path   TEXT NOT NULL,             -- readable form, not unique by itself
  raw_path        BYTEA,                     -- only set when the path isn't valid UTF-8
  size            BIGINT NOT NULL,
  mtime           TIMESTAMPTZ NOT NULL,
  mode            INTEGER NOT NULL,
  content_hash    TEXT,
  hash_algo       TEXT,
  status          TEXT NOT NULL,             -- hashed | error
  error_message   TEXT,
  scanned_at      TIMESTAMPTZ NOT NULL,
  row_version     BIGINT NOT NULL,
  PRIMARY KEY (drive_pk, path_key)
);
CREATE INDEX agent_files_hash ON agent_files (drive_pk, content_hash);

CREATE TABLE agent_dir_listings (
  drive_pk        BIGINT NOT NULL REFERENCES agent_drives(id) ON DELETE CASCADE,
  entry_key       BYTEA NOT NULL,            -- sha256(raw parent || 0x00 || raw child)
  parent_key      BYTEA NOT NULL,            -- sha256(raw parent), to list a directory's children
  relative_path   TEXT NOT NULL,             -- parent directory ('' = drive_root)
  child_name      TEXT NOT NULL,
  raw_path        BYTEA,                     -- only set when the parent path isn't valid UTF-8
  raw_child_name  BYTEA,                     -- only set when the child name isn't valid UTF-8
  is_dir          BOOLEAN NOT NULL,
  first_seen_at   TIMESTAMPTZ NOT NULL,
  row_version     BIGINT NOT NULL,
  PRIMARY KEY (drive_pk, entry_key)
);
CREATE INDEX agent_dir_listings_parent ON agent_dir_listings (drive_pk, parent_key);

CREATE TABLE agent_scan_runs (
  drive_pk        BIGINT NOT NULL REFERENCES agent_drives(id) ON DELETE CASCADE,
  run_id          BIGINT NOT NULL,           -- the agent's local scan_runs.id
  started_at      TIMESTAMPTZ NOT NULL,
  finished_at     TIMESTAMPTZ,
  files_seen      BIGINT,
  bytes_hashed    BIGINT,
  interrupted     BOOLEAN,
  row_version     BIGINT NOT NULL,
  PRIMARY KEY (drive_pk, run_id)
);

CREATE TABLE agent_tombstones (             -- deletions, kept so an older upsert can't resurrect a key
  drive_pk        BIGINT NOT NULL REFERENCES agent_drives(id) ON DELETE CASCADE,
  kind            TEXT NOT NULL,             -- 'file' | 'dir_child'
  key             BYTEA NOT NULL,            -- path_key or entry_key of the deleted row
  row_version     BIGINT NOT NULL,
  PRIMARY KEY (drive_pk, kind, key)
);
CREATE INDEX agent_tombstones_gc ON agent_tombstones (drive_pk, row_version);   -- housekeeping

CREATE TABLE agent_sync_ranges (            -- acked version intervals (from, to], merged, non-overlapping
  drive_pk        BIGINT NOT NULL REFERENCES agent_drives(id) ON DELETE CASCADE,
  from_version    BIGINT NOT NULL,
  to_version      BIGINT NOT NULL,
  PRIMARY KEY (drive_pk, from_version),
  CHECK (from_version < to_version)
);
