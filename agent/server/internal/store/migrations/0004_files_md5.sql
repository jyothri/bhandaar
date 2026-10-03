-- MD5 of each hashed file, next to its BLAKE3 content_hash, so agent drives
-- can be matched with Google Drive and Cloud Storage (docs/specs/duplicates.md,
-- "MD5 in driveagent"). Sent by driveagent 0.6.0+; NULL until a file is
-- re-read by one.

ALTER TABLE agent_files ADD COLUMN md5 TEXT;
CREATE INDEX agent_files_md5 ON agent_files (drive_pk, md5) WHERE md5 IS NOT NULL;
