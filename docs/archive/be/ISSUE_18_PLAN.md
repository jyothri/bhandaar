# Issue #18 Implementation Plan: No Prepared Statements

**Document Version:** 1.0
**Created:** 2025-12-21
**Status:** Planning Phase
**Priority:** P2 - Medium Priority (Performance Optimization)

---

## Executive Summary

This document provides a comprehensive implementation plan to address **Issue #18: No Prepared Statements**. The current system parses every SQL query fresh each time, preventing the database from caching query plans and degrading performance at scale.

**Selected Approach:**
- **Scope**: Comprehensive - all ~27 database functions use prepared statements
- **Management**: Queries struct pattern with all statements initialized at startup
- **Context Integration**: All prepared statements use ExecContext/QueryContext with getDefaultContext() from Issue #17
- **Backward Compatibility**: Replace existing functions entirely (breaking changes acceptable)
- **Transaction Support**: Prepared statements work with both db and tx (transaction-aware)
- **Lifecycle**: Initialize all on startup, close on shutdown (fail-fast if any fail)
- **Error Handling**: Fail startup if any statement preparation fails
- **Testing**: Manual testing with profiling
- **Monitoring**: Log preparation success/failure
- **Prioritization**: Both INSERT and SELECT operations

**Estimated Effort:** 1 day (8 hours)

**Impact:**
- 10-30% performance improvement for repeated queries
- Reduced database server CPU usage
- Better query plan caching
- Protection against SQL injection (already good, but reinforced)
- Clean separation of query logic

---

## Table of Contents

1. [Current State Analysis](#1-current-state-analysis)
2. [Target Architecture](#2-target-architecture)
3. [Implementation Details](#3-implementation-details)
4. [Testing Strategy](#4-testing-strategy)
5. [Deployment Plan](#5-deployment-plan)
6. [Integration with Issue #17](#6-integration-with-issue-17)

---

## 1. Current State Analysis

### 1.1 Current Implementation

**All 27 database functions parse queries on every call:**

```go
func LogStartScan(scanType string) (int, error) {
	insert_row := `insert into scans (scan_type, created_on, scan_start_time)
	               values ($1, current_timestamp, current_timestamp) RETURNING id`
	lastInsertId := 0
	err := db.QueryRow(insert_row, scanType).Scan(&lastInsertId)
	// Query parsed EVERY time this function is called
	return lastInsertId, err
}

func GetScanById(scanId int) (*Scan, error) {
	read_row := `select id, scan_type, COALESCE(status, 'Completed') as status,
		error_msg, completed_at FROM scans WHERE id = $1`
	var scan Scan
	err := db.Get(&scan, read_row, scanId)
	// Query parsed EVERY time - no caching
	return &scan, nil
}
```

### 1.2 Function Inventory

**Database Functions (27 total):**

| Category | Function | Query Type | Frequency | Priority |
|----------|----------|------------|-----------|----------|
| **Scan Management** | LogStartScan | INSERT | High | Critical |
| | MarkScanCompleted | UPDATE | High | Critical |
| | MarkScanFailed | UPDATE | High | Critical |
| | GetScanById | SELECT | High | Critical |
| | GetScansFromDb | SELECT | Medium | High |
| | DeleteScan | DELETE (7 queries) | Low | Medium |
| **Scan Metadata** | SaveScanMetadata | INSERT | Medium | High |
| | GetScanRequestsFromDb | SELECT | Medium | Medium |
| **Scan Data** | SaveStatToDb | INSERT (loop) | Very High | Critical |
| | GetScanDataFromDb | SELECT | Medium | High |
| **Message Metadata** | SaveMessageMetadataToDb | INSERT (loop) | Very High | Critical |
| | GetMessageMetadataFromDb | SELECT | Medium | High |
| **Photos** | SavePhotosMediaItemToDb | INSERT (loop) | Very High | Critical |
| | GetPhotosMediaItemFromDb | SELECT | Medium | High |
| **OAuth** | SaveOAuthToken | INSERT | Low | Medium |
| | GetOAuthToken | SELECT | Medium | High |
| **Accounts** | GetRequestAccountsFromDb | SELECT | Low | Medium |
| | GetAccountsFromDb | SELECT | Low | Medium |
| **Migrations** | migrateDB | Various | Once | Low |
| | migrateDBv0 | Various | Once | Low |
| | migrateAddStatusColumn | DDL | Once | Low |

**High-Frequency Operations (Top Priority):**
1. **SaveStatToDb** - Called in loop for every file in local scan
2. **SaveMessageMetadataToDb** - Called in loop for every Gmail message
3. **SavePhotosMediaItemToDb** - Called in loop for every photo/video
4. **GetScanById** - Called frequently to check scan status
5. **LogStartScan** - Called for every scan start
6. **MarkScanCompleted/MarkScanFailed** - Called for every scan end

### 1.3 Performance Impact

**Without Prepared Statements:**

```
Timeline for a Gmail scan with 1,000 messages:
1. First message insert: Parse query (5ms) + Execute (2ms) = 7ms
2. Second message insert: Parse query (5ms) + Execute (2ms) = 7ms
3. ... 998 more times ...
1000. Last message insert: Parse query (5ms) + Execute (2ms) = 7ms

Total parsing overhead: 1,000 × 5ms = 5 seconds wasted
Database CPU: High (repeated parsing)
```

**With Prepared Statements:**

```
Timeline for a Gmail scan with 1,000 messages:
1. Startup: Parse query once (5ms)
2. First message insert: Execute (2ms)
3. Second message insert: Execute (2ms)
4. ... 998 more times ...
1000. Last message insert: Execute (2ms)

Total parsing overhead: 5ms (once)
Database CPU: Low (cached query plan)
Savings: ~5 seconds per 1,000 messages
```

### 1.4 Current vs Target

| Aspect | Current | Target |
|--------|---------|--------|
| **Query parsing** | Every call | Once at startup |
| **Database CPU** | High (repeated parsing) | Low (cached plans) |
| **Query plan cache** | Not utilized | Fully utilized |
| **Startup time** | ~50ms | ~200ms (preparation overhead) |
| **Query execution** | 5-10ms per query | 2-5ms per query |
| **Memory usage** | Low | +2-5 MB (prepared statements) |
| **Code organization** | Inline SQL strings | Centralized Queries struct |

---

## 2. Target Architecture

### 2.1 Queries Struct Pattern

```
┌─────────────────────────────────────────────────────────────┐
│ 1. Application Startup                                      │
│    - SetupDatabase() connects to PostgreSQL                 │
│    - Run migrations                                          │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 2. PrepareQueries()                                         │
│    - Create Queries struct instance                         │
│    - Prepare all 27 statements                              │
│    - If any fail → return error → startup fails            │
│    - Store in global variable                               │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 3. Normal Operations                                        │
│    - Functions call queries.StmtName.ExecContext()          │
│    - Use getDefaultContext() for 30s timeout                │
│    - Database uses cached query plan                        │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 4. Transaction Support                                      │
│    - tx.Stmt(queries.StmtName) creates tx-scoped stmt      │
│    - Execute within transaction                             │
│    - Same prepared statement, different connection          │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 5. Graceful Shutdown                                        │
│    - CloseQueries() closes all prepared statements          │
│    - CloseDatabase() closes connection pool                 │
└─────────────────────────────────────────────────────────────┘
```

### 2.2 Queries Struct Design

```go
type Queries struct {
	// Scan Management
	insertScan       *sqlx.Stmt
	updateScanComplete *sqlx.Stmt
	updateScanFailed   *sqlx.Stmt
	selectScanById     *sqlx.Stmt
	selectScans        *sqlx.Stmt
	countScans         *sqlx.Stmt

	// Scan Metadata
	insertScanMetadata *sqlx.Stmt
	selectScanRequests *sqlx.Stmt

	// Scan Data
	insertScanData   *sqlx.Stmt
	selectScanData   *sqlx.Stmt
	countScanData    *sqlx.Stmt

	// Message Metadata
	checkDuplicateMessage *sqlx.Stmt
	insertMessageMetadata *sqlx.Stmt
	selectMessageMetadata *sqlx.Stmt
	countMessageMetadata  *sqlx.Stmt

	// Photos Media Item
	insertPhotosMediaItem *sqlx.Stmt
	insertPhotoMetadata   *sqlx.Stmt
	insertVideoMetadata   *sqlx.Stmt
	selectPhotosMediaItem *sqlx.Stmt
	countPhotosMediaItem  *sqlx.Stmt

	// OAuth Tokens
	insertOAuthToken *sqlx.Stmt
	selectOAuthToken *sqlx.Stmt

	// Accounts
	selectRequestAccounts *sqlx.Stmt
	selectAccounts        *sqlx.Stmt

	// Delete Operations
	deleteScanData        *sqlx.Stmt
	deleteMessageMetadata *sqlx.Stmt
	deleteScanMetadata    *sqlx.Stmt
	deletePhotoMetadata   *sqlx.Stmt
	deleteVideoMetadata   *sqlx.Stmt
	deletePhotosMediaItem *sqlx.Stmt
	deleteScans           *sqlx.Stmt
}
```

### 2.3 Transaction-Aware Usage

```go
// Direct database use
func GetScanById(scanId int) (*Scan, error) {
	ctx, cancel := getDefaultContext()
	defer cancel()

	var scan Scan
	err := queries.selectScanById.GetContext(ctx, &scan, scanId)
	return &scan, err
}

// Transaction use
func SavePhotosMediaItemToDb(scanId int, photosMediaItem <-chan PhotosMediaItem) {
	for pmi := range photosMediaItem {
		tx, err := db.Beginx()
		if err != nil {
			continue
		}

		// Use tx.Stmt() to get transaction-scoped statement
		txStmt := tx.Stmtx(queries.insertPhotosMediaItem)

		ctx, cancel := getDefaultContext()
		lastInsertId := 0
		err = txStmt.QueryRowContext(ctx, pmi.MediaItemId, ...).Scan(&lastInsertId)
		cancel()

		if err != nil {
			tx.Rollback()
			continue
		}

		// Use other tx statements...
		tx.Commit()
	}
}
```

---

## 3. Implementation Details

### 3.1 Enhanced Database Setup: `db/database.go`

**Add global queries variable:**

```go
package db

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

const (
	host     = "hdd_db"
	port     = 5432
	user     = "hddb"
	password = "hddb"
	dbname   = "hdd_db"
)

var db *sqlx.DB
var queries *Queries  // NEW: Global prepared statements

// Queries holds all prepared statements
type Queries struct {
	// Scan Management
	insertScan         *sqlx.Stmt
	updateScanComplete *sqlx.Stmt
	updateScanFailed   *sqlx.Stmt
	selectScanById     *sqlx.Stmt
	selectScans        *sqlx.Stmt
	countScans         *sqlx.Stmt

	// Scan Metadata
	insertScanMetadata *sqlx.Stmt
	selectScanRequests *sqlx.Stmt

	// Scan Data
	insertScanData *sqlx.Stmt
	selectScanData *sqlx.Stmt
	countScanData  *sqlx.Stmt

	// Message Metadata
	checkDuplicateMessage *sqlx.Stmt
	insertMessageMetadata *sqlx.Stmt
	selectMessageMetadata *sqlx.Stmt
	countMessageMetadata  *sqlx.Stmt

	// Photos Media Item
	insertPhotosMediaItem *sqlx.Stmt
	insertPhotoMetadata   *sqlx.Stmt
	insertVideoMetadata   *sqlx.Stmt
	selectPhotosMediaItem *sqlx.Stmt
	countPhotosMediaItem  *sqlx.Stmt

	// OAuth Tokens
	insertOAuthToken *sqlx.Stmt
	selectOAuthToken *sqlx.Stmt

	// Accounts
	selectRequestAccounts *sqlx.Stmt
	selectAccounts        *sqlx.Stmt

	// Delete Operations
	deleteScanData        *sqlx.Stmt
	deleteMessageMetadata *sqlx.Stmt
	deleteScanMetadata    *sqlx.Stmt
	deletePhotoMetadata   *sqlx.Stmt
	deleteVideoMetadata   *sqlx.Stmt
	deletePhotosMediaItem *sqlx.Stmt
	deleteScans           *sqlx.Stmt
}
```

**Update SetupDatabase():**

```go
func SetupDatabase() error {
	psqlInfo := fmt.Sprintf("host=%s port=%d user=%s "+
		"password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)

	var err error
	db, err = sqlx.Open("postgres", psqlInfo)
	if err != nil {
		return fmt.Errorf("failed to open database connection: %w", err)
	}

	err = db.Ping()
	if err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	slog.Info("Successfully connected to database")

	// Run migrations first
	if err := migrateDB(); err != nil {
		return fmt.Errorf("failed to run database migrations: %w", err)
	}

	// NEW: Prepare all statements
	if err := prepareQueries(); err != nil {
		return fmt.Errorf("failed to prepare queries: %w", err)
	}

	return nil
}
```

**Add prepareQueries():**

```go
// prepareQueries prepares all SQL statements at startup
// If any statement fails to prepare, startup fails (fail-fast)
func prepareQueries() error {
	slog.Info("Preparing database queries...")

	q := &Queries{}
	var err error

	// Scan Management Statements
	q.insertScan, err = db.Preparex(`
		INSERT INTO scans (scan_type, created_on, scan_start_time)
		VALUES ($1, current_timestamp, current_timestamp)
		RETURNING id`)
	if err != nil {
		return fmt.Errorf("failed to prepare insertScan: %w", err)
	}

	q.updateScanComplete, err = db.Preparex(`
		UPDATE scans
		SET scan_end_time = current_timestamp, status = 'Completed'
		WHERE id = $1`)
	if err != nil {
		return fmt.Errorf("failed to prepare updateScanComplete: %w", err)
	}

	q.updateScanFailed, err = db.Preparex(`
		UPDATE scans
		SET scan_end_time = current_timestamp, status = 'Failed', error_msg = $2
		WHERE id = $1`)
	if err != nil {
		return fmt.Errorf("failed to prepare updateScanFailed: %w", err)
	}

	q.selectScanById, err = db.Preparex(`
		SELECT id, scan_type, COALESCE(status, 'Completed') as status,
			error_msg, completed_at
		FROM scans
		WHERE id = $1`)
	if err != nil {
		return fmt.Errorf("failed to prepare selectScanById: %w", err)
	}

	q.selectScans, err = db.Preparex(`
		SELECT S.id, scan_type,
			created_on AT TIME ZONE 'UTC' AT TIME ZONE 'America/Los_Angeles' as created_on,
			scan_start_time AT TIME ZONE 'UTC' AT TIME ZONE 'America/Los_Angeles' as scan_start_time,
			scan_end_time, CONCAT(search_path, search_filter) as metadata,
			date_trunc('millisecond', COALESCE(scan_end_time, current_timestamp) - scan_start_time) as duration
		FROM scans S
		LEFT JOIN scanmetadata SM ON S.id = SM.scan_id
		ORDER BY id
		LIMIT $1 OFFSET $2`)
	if err != nil {
		return fmt.Errorf("failed to prepare selectScans: %w", err)
	}

	q.countScans, err = db.Preparex(`
		SELECT count(*) FROM scans`)
	if err != nil {
		return fmt.Errorf("failed to prepare countScans: %w", err)
	}

	// Scan Metadata Statements
	q.insertScanMetadata, err = db.Preparex(`
		INSERT INTO scanmetadata (name, search_path, search_filter, scan_id)
		VALUES ($1, $2, $3, $4)
		RETURNING id`)
	if err != nil {
		return fmt.Errorf("failed to prepare insertScanMetadata: %w", err)
	}

	q.selectScanRequests, err = db.Preparex(`
		SELECT DISTINCT COALESCE(sm.name, '') as name, sm.search_filter, s.id,
			s.scan_type,
			scan_start_time AT TIME ZONE 'UTC' AT TIME ZONE 'America/Los_Angeles' as scan_start_time,
			COALESCE(EXTRACT(EPOCH FROM (scan_end_time - scan_start_time)), -1) as scan_duration_in_sec
		FROM scans s
		JOIN scanmetadata sm ON sm.scan_id = s.id
		WHERE sm.name = $1
		GROUP BY sm.name, sm.search_filter, s.id, s.scan_start_time, s.scan_type
		ORDER BY s.id DESC`)
	if err != nil {
		return fmt.Errorf("failed to prepare selectScanRequests: %w", err)
	}

	// Scan Data Statements
	q.insertScanData, err = db.Preparex(`
		INSERT INTO scandata (name, path, size, file_mod_time, md5hash, scan_id, is_dir, file_count)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`)
	if err != nil {
		return fmt.Errorf("failed to prepare insertScanData: %w", err)
	}

	q.selectScanData, err = db.Preparex(`
		SELECT * FROM scandata
		WHERE scan_id = $1
		ORDER BY id
		LIMIT $2 OFFSET $3`)
	if err != nil {
		return fmt.Errorf("failed to prepare selectScanData: %w", err)
	}

	q.countScanData, err = db.Preparex(`
		SELECT count(*) FROM scandata WHERE scan_id = $1`)
	if err != nil {
		return fmt.Errorf("failed to prepare countScanData: %w", err)
	}

	// Message Metadata Statements
	q.checkDuplicateMessage, err = db.Preparex(`
		SELECT count(*)
		FROM messagemetadata
		WHERE username = $1 AND message_id = $2 AND thread_id = $3`)
	if err != nil {
		return fmt.Errorf("failed to prepare checkDuplicateMessage: %w", err)
	}

	q.insertMessageMetadata, err = db.Preparex(`
		INSERT INTO messagemetadata
			(message_id, thread_id, date, mail_from, mail_to, subject, size_estimate, labels, scan_id, username)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id`)
	if err != nil {
		return fmt.Errorf("failed to prepare insertMessageMetadata: %w", err)
	}

	q.selectMessageMetadata, err = db.Preparex(`
		SELECT id, message_id, thread_id, date, mail_from, mail_to,
			subject, size_estimate, labels, scan_id
		FROM messagemetadata
		WHERE scan_id = $1
		ORDER BY id
		LIMIT $2 OFFSET $3`)
	if err != nil {
		return fmt.Errorf("failed to prepare selectMessageMetadata: %w", err)
	}

	q.countMessageMetadata, err = db.Preparex(`
		SELECT count(*) FROM messagemetadata WHERE scan_id = $1`)
	if err != nil {
		return fmt.Errorf("failed to prepare countMessageMetadata: %w", err)
	}

	// Photos Media Item Statements
	q.insertPhotosMediaItem, err = db.Preparex(`
		INSERT INTO photosmediaitem
			(media_item_id, product_url, mime_type, filename, size, scan_id, file_mod_time,
			 contributor_display_name, md5hash)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING id`)
	if err != nil {
		return fmt.Errorf("failed to prepare insertPhotosMediaItem: %w", err)
	}

	q.insertPhotoMetadata, err = db.Preparex(`
		INSERT INTO photometadata
			(photos_media_item_id, camera_make, camera_model, focal_length, f_number, iso, exposure_time)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`)
	if err != nil {
		return fmt.Errorf("failed to prepare insertPhotoMetadata: %w", err)
	}

	q.insertVideoMetadata, err = db.Preparex(`
		INSERT INTO videometadata
			(photos_media_item_id, camera_make, camera_model, fps)
		VALUES ($1, $2, $3, $4)
		RETURNING id`)
	if err != nil {
		return fmt.Errorf("failed to prepare insertVideoMetadata: %w", err)
	}

	q.selectPhotosMediaItem, err = db.Preparex(`
		SELECT id, media_item_id, product_url, mime_type, filename,
			size, file_mod_time, md5hash, scan_id, contributor_display_name
		FROM photosmediaitem
		WHERE scan_id = $1
		ORDER BY id
		LIMIT $2 OFFSET $3`)
	if err != nil {
		return fmt.Errorf("failed to prepare selectPhotosMediaItem: %w", err)
	}

	q.countPhotosMediaItem, err = db.Preparex(`
		SELECT count(*) FROM photosmediaitem WHERE scan_id = $1`)
	if err != nil {
		return fmt.Errorf("failed to prepare countPhotosMediaItem: %w", err)
	}

	// OAuth Token Statements
	q.insertOAuthToken, err = db.Preparex(`
		INSERT INTO privatetokens
			(access_token, refresh_token, display_name, client_key, scope, expires_in, token_type, created_on)
		VALUES ($1, $2, $3, $4, $5, $6, $7, current_timestamp)
		RETURNING id`)
	if err != nil {
		return fmt.Errorf("failed to prepare insertOAuthToken: %w", err)
	}

	q.selectOAuthToken, err = db.Preparex(`
		SELECT id, access_token, refresh_token, display_name, client_key, created_on, scope, expires_in, token_type
		FROM privatetokens
		WHERE client_key = $1`)
	if err != nil {
		return fmt.Errorf("failed to prepare selectOAuthToken: %w", err)
	}

	// Account Statements
	q.selectRequestAccounts, err = db.Preparex(`
		SELECT DISTINCT display_name, client_key
		FROM privatetokens`)
	if err != nil {
		return fmt.Errorf("failed to prepare selectRequestAccounts: %w", err)
	}

	q.selectAccounts, err = db.Preparex(`
		SELECT DISTINCT name
		FROM scanmetadata
		WHERE name IS NOT NULL
		ORDER BY 1`)
	if err != nil {
		return fmt.Errorf("failed to prepare selectAccounts: %w", err)
	}

	// Delete Statements (for DeleteScan transaction)
	q.deleteScanData, err = db.Preparex(`DELETE FROM scandata WHERE scan_id = $1`)
	if err != nil {
		return fmt.Errorf("failed to prepare deleteScanData: %w", err)
	}

	q.deleteMessageMetadata, err = db.Preparex(`DELETE FROM messagemetadata WHERE scan_id = $1`)
	if err != nil {
		return fmt.Errorf("failed to prepare deleteMessageMetadata: %w", err)
	}

	q.deleteScanMetadata, err = db.Preparex(`DELETE FROM scanmetadata WHERE scan_id = $1`)
	if err != nil {
		return fmt.Errorf("failed to prepare deleteScanMetadata: %w", err)
	}

	q.deletePhotoMetadata, err = db.Preparex(`
		DELETE FROM photometadata
		WHERE photos_media_item_id IN (
			SELECT id FROM photosmediaitem WHERE scan_id = $1
		)`)
	if err != nil {
		return fmt.Errorf("failed to prepare deletePhotoMetadata: %w", err)
	}

	q.deleteVideoMetadata, err = db.Preparex(`
		DELETE FROM videometadata
		WHERE photos_media_item_id IN (
			SELECT id FROM photosmediaitem WHERE scan_id = $1
		)`)
	if err != nil {
		return fmt.Errorf("failed to prepare deleteVideoMetadata: %w", err)
	}

	q.deletePhotosMediaItem, err = db.Preparex(`DELETE FROM photosmediaitem WHERE scan_id = $1`)
	if err != nil {
		return fmt.Errorf("failed to prepare deletePhotosMediaItem: %w", err)
	}

	q.deleteScans, err = db.Preparex(`DELETE FROM scans WHERE id = $1`)
	if err != nil {
		return fmt.Errorf("failed to prepare deleteScans: %w", err)
	}

	// Store globally
	queries = q

	slog.Info("Successfully prepared all database queries",
		"statement_count", 27)

	return nil
}
```

**Add closeQueries():**

```go
// closeQueries closes all prepared statements
func closeQueries() error {
	if queries == nil {
		return nil
	}

	slog.Info("Closing prepared statements...")

	statements := []*sqlx.Stmt{
		queries.insertScan,
		queries.updateScanComplete,
		queries.updateScanFailed,
		queries.selectScanById,
		queries.selectScans,
		queries.countScans,
		queries.insertScanMetadata,
		queries.selectScanRequests,
		queries.insertScanData,
		queries.selectScanData,
		queries.countScanData,
		queries.checkDuplicateMessage,
		queries.insertMessageMetadata,
		queries.selectMessageMetadata,
		queries.countMessageMetadata,
		queries.insertPhotosMediaItem,
		queries.insertPhotoMetadata,
		queries.insertVideoMetadata,
		queries.selectPhotosMediaItem,
		queries.countPhotosMediaItem,
		queries.insertOAuthToken,
		queries.selectOAuthToken,
		queries.selectRequestAccounts,
		queries.selectAccounts,
		queries.deleteScanData,
		queries.deleteMessageMetadata,
		queries.deleteScanMetadata,
		queries.deletePhotoMetadata,
		queries.deleteVideoMetadata,
		queries.deletePhotosMediaItem,
		queries.deleteScans,
	}

	var lastErr error
	closedCount := 0

	for _, stmt := range statements {
		if stmt != nil {
			if err := stmt.Close(); err != nil {
				slog.Error("Failed to close prepared statement", "error", err)
				lastErr = err
			} else {
				closedCount++
			}
		}
	}

	slog.Info("Closed prepared statements", "closed", closedCount, "total", len(statements))

	if lastErr != nil {
		return fmt.Errorf("failed to close some statements: %w", lastErr)
	}

	return nil
}
```

**Update Close():**

```go
// Close closes prepared statements and database connection
func Close() error {
	// Close prepared statements first
	if err := closeQueries(); err != nil {
		slog.Error("Error closing prepared statements", "error", err)
		// Continue to close database anyway
	}

	// Close database connection
	if db != nil {
		if err := db.Close(); err != nil {
			return fmt.Errorf("failed to close database: %w", err)
		}
		slog.Info("Database connection closed")
	}

	return nil
}
```

**Add getDefaultContext() helper (from Issue #17):**

```go
// getDefaultContext returns a context with 30-second timeout for queries
func getDefaultContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30*time.Second)
}
```

### 3.2 Update Database Functions

**Update LogStartScan:**

```go
// Before:
func LogStartScan(scanType string) (int, error) {
	insert_row := `insert into scans
									(scan_type, created_on, scan_start_time)
								values
									($1, current_timestamp, current_timestamp) RETURNING id`
	lastInsertId := 0
	err := db.QueryRow(insert_row, scanType).Scan(&lastInsertId)
	if err != nil {
		return 0, fmt.Errorf("failed to insert scan for type %s: %w", scanType, err)
	}
	return lastInsertId, nil
}

// After:
func LogStartScan(scanType string) (int, error) {
	ctx, cancel := getDefaultContext()
	defer cancel()

	lastInsertId := 0
	err := queries.insertScan.QueryRowContext(ctx, scanType).Scan(&lastInsertId)
	if err != nil {
		return 0, fmt.Errorf("failed to insert scan for type %s: %w", scanType, err)
	}
	return lastInsertId, nil
}
```

**Update MarkScanCompleted:**

```go
// After:
func MarkScanCompleted(scanId int) error {
	ctx, cancel := getDefaultContext()
	defer cancel()

	res, err := queries.updateScanComplete.ExecContext(ctx, scanId)
	if err != nil {
		return fmt.Errorf("failed to mark scan %d as completed: %w", scanId, err)
	}
	count, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected for scan %d: %w", scanId, err)
	}
	if count != 1 {
		slog.Warn("Unexpected rows affected when marking scan complete",
			"scan_id", scanId,
			"expected", 1,
			"actual", count)
	}
	slog.Info("Scan marked as completed", "scan_id", scanId)
	return nil
}
```

**Update MarkScanFailed:**

```go
// After:
func MarkScanFailed(scanId int, errMsg string) error {
	ctx, cancel := getDefaultContext()
	defer cancel()

	res, err := queries.updateScanFailed.ExecContext(ctx, scanId, errMsg)
	if err != nil {
		return fmt.Errorf("failed to mark scan %d as failed: %w", scanId, err)
	}
	count, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected for scan %d: %w", scanId, err)
	}
	if count != 1 {
		slog.Warn("Unexpected rows affected when marking scan failed",
			"scan_id", scanId,
			"expected", 1,
			"actual", count)
	}
	slog.Error("Scan marked as failed", "scan_id", scanId, "error", errMsg)
	return nil
}
```

**Update GetScanById:**

```go
// After:
func GetScanById(scanId int) (*Scan, error) {
	ctx, cancel := getDefaultContext()
	defer cancel()

	var scan Scan
	err := queries.selectScanById.GetContext(ctx, &scan, scanId)
	if err != nil {
		return nil, fmt.Errorf("failed to get scan %d: %w", scanId, err)
	}

	return &scan, nil
}
```

**Update SaveScanMetadata:**

```go
// After:
func SaveScanMetadata(name string, searchPath string, searchFilter string, scanId int) error {
	ctx, cancel := getDefaultContext()
	defer cancel()

	_, err := queries.insertScanMetadata.ExecContext(ctx, name, searchPath, searchFilter, scanId)
	if err != nil {
		return fmt.Errorf("failed to save scan metadata for scan %d (name=%s, path=%s): %w",
			scanId, name, searchPath, err)
	}
	return nil
}
```

**Update SaveMessageMetadataToDb (with prepared statements):**

```go
// After:
func SaveMessageMetadataToDb(scanId int, username string, messageMetaData <-chan MessageMetadata) {
	for {
		mmd, more := <-messageMetaData
		if !more {
			// Channel closed - mark scan as complete if not already failed
			scan, err := GetScanById(scanId)
			if err != nil {
				slog.Error("Failed to get scan status",
					"scan_id", scanId,
					"error", err)
				return
			}

			if scan.Status != "Failed" {
				if err := MarkScanCompleted(scanId); err != nil {
					slog.Error("Failed to mark scan complete",
						"scan_id", scanId,
						"error", err)
				}
			}
			break
		}

		// Check for duplicates using prepared statement
		ctx1, cancel1 := getDefaultContext()
		var count int
		err := queries.checkDuplicateMessage.GetContext(ctx1, &count, username, mmd.MessageId, mmd.ThreadId)
		cancel1()

		if err != nil {
			slog.Error("Failed to check for duplicate message, skipping",
				"scan_id", scanId,
				"message_id", mmd.MessageId,
				"username", username,
				"error", err)
			continue
		}
		if count > 0 {
			continue
		}

		// Insert using prepared statement
		ctx2, cancel2 := getDefaultContext()
		_, err = queries.insertMessageMetadata.ExecContext(ctx2,
			mmd.MessageId, mmd.ThreadId, mmd.Date.UTC(), substr(mmd.From, 500),
			substr(mmd.To, 500), substr(mmd.Subject, 2000), mmd.SizeEstimate,
			substr(strings.Join(mmd.LabelIds, ","), 500), scanId, username)
		cancel2()

		if err != nil {
			slog.Error("Failed to save message metadata, skipping",
				"scan_id", scanId,
				"message_id", mmd.MessageId,
				"username", username,
				"subject", substr(mmd.Subject, 50),
				"size_bytes", mmd.SizeEstimate,
				"error", err)
			continue
		}
	}
}
```

**Update SavePhotosMediaItemToDb (transaction-aware):**

```go
// After:
func SavePhotosMediaItemToDb(scanId int, photosMediaItem <-chan PhotosMediaItem) {
	for {
		pmi, more := <-photosMediaItem
		if !more {
			// Channel closed - mark scan as complete if not already failed
			scan, err := GetScanById(scanId)
			if err != nil {
				slog.Error("Failed to get scan status",
					"scan_id", scanId,
					"error", err)
				return
			}

			if scan.Status != "Failed" {
				if err := MarkScanCompleted(scanId); err != nil {
					slog.Error("Failed to mark scan complete",
						"scan_id", scanId,
						"error", err)
				}
			}
			break
		}

		// Use transaction for parent + children (atomicity required)
		tx, err := db.Beginx()
		if err != nil {
			slog.Error("Failed to begin transaction for photos media item, skipping",
				"scan_id", scanId,
				"media_item_id", pmi.MediaItemId,
				"error", err)
			continue
		}

		// Use transaction-scoped prepared statement
		txStmt := tx.Stmtx(queries.insertPhotosMediaItem)

		ctx, cancel := getDefaultContext()
		lastInsertId := 0
		err = txStmt.QueryRowContext(ctx, pmi.MediaItemId, pmi.ProductUrl, pmi.MimeType, pmi.Filename,
			pmi.Size, scanId, pmi.FileModTime, pmi.ContributorDisplayName, pmi.Md5hash).Scan(&lastInsertId)
		cancel()

		if err != nil {
			tx.Rollback()
			slog.Error("Failed to insert photos media item, skipping",
				"scan_id", scanId,
				"media_item_id", pmi.MediaItemId,
				"filename", pmi.Filename,
				"error", err)
			continue
		}

		switch pmi.MimeType[:5] {
		case "image":
			txPhotoStmt := tx.Stmtx(queries.insertPhotoMetadata)
			ctx2, cancel2 := getDefaultContext()
			_, err = txPhotoStmt.ExecContext(ctx2, lastInsertId, pmi.CameraMake, pmi.CameraModel, pmi.FocalLength,
				pmi.FNumber, pmi.Iso, pmi.ExposureTime)
			cancel2()

			if err != nil {
				tx.Rollback()
				slog.Error("Failed to insert photo metadata, skipping",
					"scan_id", scanId,
					"media_item_id", pmi.MediaItemId,
					"camera", fmt.Sprintf("%s %s", pmi.CameraMake, pmi.CameraModel),
					"error", err)
				continue
			}
		case "video":
			txVideoStmt := tx.Stmtx(queries.insertVideoMetadata)
			ctx3, cancel3 := getDefaultContext()
			_, err = txVideoStmt.ExecContext(ctx3, lastInsertId, pmi.CameraMake, pmi.CameraModel, pmi.Fps)
			cancel3()

			if err != nil {
				tx.Rollback()
				slog.Error("Failed to insert video metadata, skipping",
					"scan_id", scanId,
					"media_item_id", pmi.MediaItemId,
					"fps", pmi.Fps,
					"error", err)
				continue
			}
		default:
			slog.Warn("Unsupported mime type",
				"mime_type", pmi.MimeType,
				"media_item_id", pmi.MediaItemId)
		}

		if err := tx.Commit(); err != nil {
			slog.Error("Failed to commit transaction for photos media item, skipping",
				"scan_id", scanId,
				"media_item_id", pmi.MediaItemId,
				"error", err)
			continue
		}
	}
}
```

**Update SaveStatToDb:**

```go
// After:
func SaveStatToDb(scanId int, scanData <-chan FileData) {
	for {
		fd, more := <-scanData
		if !more {
			// Channel closed - mark scan as complete if not already failed
			scan, err := GetScanById(scanId)
			if err != nil {
				slog.Error("Failed to get scan status",
					"scan_id", scanId,
					"error", err)
				return
			}

			if scan.Status != "Failed" {
				if err := MarkScanCompleted(scanId); err != nil {
					slog.Error("Failed to mark scan complete",
						"scan_id", scanId,
						"error", err)
				}
			}
			break
		}

		ctx, cancel := getDefaultContext()
		var err error
		if fd.IsDir {
			_, err = queries.insertScanData.ExecContext(ctx, fd.FileName, fd.FilePath, fd.Size, fd.ModTime, fd.Md5Hash, scanId, fd.IsDir, fd.FileCount)
		} else {
			_, err = queries.insertScanData.ExecContext(ctx, fd.FileName, fd.FilePath, fd.Size, fd.ModTime, fd.Md5Hash, scanId, fd.IsDir, nil)
		}
		cancel()

		if err != nil {
			slog.Error("Failed to save file scan data, skipping",
				"scan_id", scanId,
				"path", fd.FilePath,
				"is_dir", fd.IsDir,
				"size_bytes", fd.Size,
				"error", err)
			continue
		}
	}
}
```

**Update remaining GET functions:**

```go
// GetScansFromDb
func GetScansFromDb(pageNo int) ([]Scan, int, error) {
	limit := 10
	offset := limit * (pageNo - 1)

	ctx1, cancel1 := getDefaultContext()
	defer cancel1()

	scans := []Scan{}
	err := queries.selectScans.SelectContext(ctx1, &scans, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get scans for page %d: %w", pageNo, err)
	}

	ctx2, cancel2 := getDefaultContext()
	defer cancel2()

	var count int
	err = queries.countScans.GetContext(ctx2, &count)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get scan count: %w", err)
	}

	return scans, count, nil
}

// GetMessageMetadataFromDb
func GetMessageMetadataFromDb(scanId int, pageNo int) ([]MessageMetadataRead, int, error) {
	limit := 10
	offset := limit * (pageNo - 1)

	ctx1, cancel1 := getDefaultContext()
	defer cancel1()

	var count int
	err := queries.countMessageMetadata.GetContext(ctx1, &count, scanId)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get message count for scan %d: %w", scanId, err)
	}

	ctx2, cancel2 := getDefaultContext()
	defer cancel2()

	messageMetadata := []MessageMetadataRead{}
	err = queries.selectMessageMetadata.SelectContext(ctx2, &messageMetadata, scanId, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get message metadata for scan %d, page %d: %w", scanId, pageNo, err)
	}

	return messageMetadata, count, nil
}

// GetPhotosMediaItemFromDb
func GetPhotosMediaItemFromDb(scanId int, pageNo int) ([]PhotosMediaItemRead, int, error) {
	limit := 10
	offset := limit * (pageNo - 1)

	ctx1, cancel1 := getDefaultContext()
	defer cancel1()

	var count int
	err := queries.countPhotosMediaItem.GetContext(ctx1, &count, scanId)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get photo count for scan %d: %w", scanId, err)
	}

	ctx2, cancel2 := getDefaultContext()
	defer cancel2()

	photosMediaItemRead := []PhotosMediaItemRead{}
	err = queries.selectPhotosMediaItem.SelectContext(ctx2, &photosMediaItemRead, scanId, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get photos for scan %d, page %d: %w", scanId, pageNo, err)
	}

	return photosMediaItemRead, count, nil
}

// GetScanDataFromDb
func GetScanDataFromDb(scanId int, pageNo int) ([]ScanData, int, error) {
	limit := 10
	offset := limit * (pageNo - 1)

	ctx1, cancel1 := getDefaultContext()
	defer cancel1()

	var count int
	err := queries.countScanData.GetContext(ctx1, &count, scanId)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get scan data count for scan %d: %w", scanId, err)
	}

	ctx2, cancel2 := getDefaultContext()
	defer cancel2()

	scandata := []ScanData{}
	err = queries.selectScanData.SelectContext(ctx2, &scandata, scanId, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get scan data for scan %d, page %d: %w", scanId, pageNo, err)
	}

	return scandata, count, nil
}

// SaveOAuthToken
func SaveOAuthToken(accessToken string, refreshToken string, displayName string, clientKey string, scope string, expiresIn int16, tokenType string) error {
	ctx, cancel := getDefaultContext()
	defer cancel()

	_, err := queries.insertOAuthToken.ExecContext(ctx, accessToken, refreshToken, displayName, clientKey, scope, expiresIn, tokenType)
	if err != nil {
		return fmt.Errorf("failed to save OAuth token for client %s: %w", clientKey, err)
	}
	return nil
}

// GetOAuthToken
func GetOAuthToken(clientKey string) (PrivateToken, error) {
	ctx, cancel := getDefaultContext()
	defer cancel()

	tokenData := PrivateToken{}
	err := queries.selectOAuthToken.GetContext(ctx, &tokenData, clientKey)
	if err != nil {
		return PrivateToken{}, fmt.Errorf("failed to get OAuth token for client %s: %w", clientKey, err)
	}
	return tokenData, nil
}

// GetRequestAccountsFromDb
func GetRequestAccountsFromDb() ([]Account, error) {
	ctx, cancel := getDefaultContext()
	defer cancel()

	accounts := []Account{}
	err := queries.selectRequestAccounts.SelectContext(ctx, &accounts)
	if err != nil {
		return nil, fmt.Errorf("failed to get request accounts: %w", err)
	}
	return accounts, nil
}

// GetAccountsFromDb
func GetAccountsFromDb() ([]string, error) {
	ctx, cancel := getDefaultContext()
	defer cancel()

	accounts := []string{}
	err := queries.selectAccounts.SelectContext(ctx, &accounts)
	if err != nil {
		return nil, fmt.Errorf("failed to get accounts: %w", err)
	}
	return accounts, nil
}

// GetScanRequestsFromDb
func GetScanRequestsFromDb(accountKey string) ([]ScanRequests, error) {
	if len(strings.TrimSpace(accountKey)) == 0 {
		return []ScanRequests{}, nil
	}

	ctx, cancel := getDefaultContext()
	defer cancel()

	scanRequests := []ScanRequests{}
	err := queries.selectScanRequests.SelectContext(ctx, &scanRequests, accountKey)
	if err != nil {
		return nil, fmt.Errorf("failed to get scan requests for account %s: %w", accountKey, err)
	}
	return scanRequests, nil
}
```

**Update DeleteScan (transaction-aware):**

```go
// After:
func DeleteScan(scanId int) error {
	// Begin transaction
	tx, err := db.Beginx()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	// Defer rollback - safe to call even after commit
	defer tx.Rollback()

	// Create context with timeout
	ctx, cancel := getDefaultContext()
	defer cancel()

	// Define tables to delete from in order
	// Use transaction-scoped statements
	deletions := []struct {
		table string
		stmt  *sqlx.Stmt
	}{
		{"scandata", tx.Stmtx(queries.deleteScanData)},
		{"messagemetadata", tx.Stmtx(queries.deleteMessageMetadata)},
		{"scanmetadata", tx.Stmtx(queries.deleteScanMetadata)},
		{"photometadata", tx.Stmtx(queries.deletePhotoMetadata)},
		{"videometadata", tx.Stmtx(queries.deleteVideoMetadata)},
		{"photosmediaitem", tx.Stmtx(queries.deletePhotosMediaItem)},
		{"scans", tx.Stmtx(queries.deleteScans)},
	}

	// Execute all deletions within transaction
	for _, deletion := range deletions {
		result, err := deletion.stmt.ExecContext(ctx, scanId)
		if err != nil {
			// Transaction automatically rolled back by defer
			return fmt.Errorf("failed to delete from %s: %w", deletion.table, err)
		}

		// Log number of rows deleted for debugging
		rowsAffected, _ := result.RowsAffected()
		slog.Debug("Deleted rows",
			"table", deletion.table,
			"rows", rowsAffected,
			"scan_id", scanId)
	}

	// Commit transaction - all deletes succeed together
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	slog.Info("Successfully deleted scan", "scan_id", scanId)
	return nil
}
```

---

## 4. Testing Strategy

### 4.1 Manual Testing with Profiling

**Test 1: Verify Prepared Statements Created**

```bash
# Start server
cd be
go run .

# Expected logs:
# INFO Preparing database queries...
# INFO Successfully prepared all database queries statement_count=27
# INFO Starting web server
```

**Test 2: Profile Query Performance**

```bash
# Create test script: test_prepared_statements.sh
#!/bin/bash

echo "Testing query performance..."

# Measure time for 100 scan status checks
START=$(date +%s%N)
for i in {1..100}; do
  curl -s http://localhost:8090/api/scans/1 > /dev/null
done
END=$(date +%s%N)
DIFF=$(( ($END - $START) / 1000000 ))
echo "100 GetScanById calls: ${DIFF}ms"
echo "Average per call: $((DIFF / 100))ms"

# Expected results:
# Without prepared statements: ~7-10ms per call
# With prepared statements: ~2-5ms per call
```

**Test 3: PostgreSQL Query Stats**

```sql
-- Connect to PostgreSQL
psql -U hddb -d hdd_db

-- Check prepared statements
SELECT name, statement, calls, total_exec_time, mean_exec_time
FROM pg_prepared_statements;

-- Expected: See all 27 prepared statements listed

-- Check query performance
SELECT query, calls, total_exec_time, mean_exec_time
FROM pg_stat_statements
WHERE query LIKE 'SELECT%scans%'
ORDER BY mean_exec_time DESC;

-- Expected: Lower mean_exec_time for prepared statement queries
```

**Test 4: Load Test with Profiling**

```bash
# Install vegeta if not already installed
go install github.com/tsenart/vegeta@latest

# Create target file
echo "GET http://localhost:8090/api/scans/1" > targets.txt

# Run load test
vegeta attack -targets=targets.txt -rate=100/s -duration=30s | vegeta report

# Expected metrics:
# Latency (mean): 2-5ms (vs 7-10ms without prepared statements)
# Success rate: 100%
```

**Test 5: Database Connection Count**

```sql
-- Check active connections
SELECT count(*) FROM pg_stat_activity WHERE datname = 'hdd_db';

-- Expected: Stable, not growing
-- Prepared statements don't create additional connections
```

**Test 6: Transaction-Aware Statements**

```bash
# Create photos scan to test transaction usage
curl -X POST http://localhost:8090/api/scans \
  -H "Content-Type: application/json" \
  -d '{
    "ScanType": "GPhotos",
    "GPhotosScan": {
      "AlbumId": "test-album",
      "RefreshToken": "...",
      "FetchSize": false,
      "FetchMd5Hash": false
    }
  }'

# Check logs for transaction commits
# Expected: No errors about prepared statement reuse in transactions
```

**Test 7: Startup Failure Handling**

```bash
# Temporarily break a query syntax
# Edit database.go and add syntax error to one prepared statement
# Example: Change `INSERT INTO` to `INSRT INTO`

go run .

# Expected:
# ERROR Failed to prepare queries: failed to prepare insertScan: syntax error
# Application exits with status 1 (fail-fast)
```

**Test 8: Memory Usage**

```bash
# Monitor memory before and after prepared statements

# Get baseline
ps aux | grep hdd | awk '{print $6}'  # Memory in KB

# Expected increase: ~2-5 MB for all prepared statements
# This is acceptable overhead for the performance gain
```

### 4.2 Profiling Commands

**CPU Profiling:**

```bash
# Start server with CPU profiling
go run . -cpuprofile=cpu.prof

# Run load test
vegeta attack -targets=targets.txt -rate=100/s -duration=30s > results.bin

# Stop server and analyze
go tool pprof cpu.prof

# In pprof:
(pprof) top10
(pprof) list SaveStatToDb

# Expected: Less time in SQL parsing, more in actual execution
```

**Benchmark Test:**

```go
// Add to db/database_test.go
package db

import (
	"testing"
)

func BenchmarkGetScanById(b *testing.B) {
	// Setup
	err := SetupDatabase()
	if err != nil {
		b.Skipf("Database not available: %v", err)
	}
	defer Close()

	// Create test scan
	scanId, _ := LogStartScan("test")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := GetScanById(scanId)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// Run:
// go test -bench=BenchmarkGetScanById -benchmem

// Expected results:
// Without prepared statements: ~7000 ns/op
// With prepared statements: ~2000 ns/op (3x faster)
```

---

## 5. Deployment Plan

### 5.1 Pre-Deployment Checklist

- [ ] Create Queries struct with all 27 statements
- [ ] Implement prepareQueries() function
- [ ] Implement closeQueries() function
- [ ] Add getDefaultContext() helper (from Issue #17)
- [ ] Update all 27 database functions to use prepared statements
- [ ] Update Close() to call closeQueries()
- [ ] Update SetupDatabase() to call prepareQueries()
- [ ] Verify builds successfully
- [ ] Run manual profiling tests
- [ ] Benchmark performance improvement
- [ ] Test transaction-aware statement usage

### 5.2 Deployment Steps

**Step 1: Update Database Package**

```bash
cd be

# Edit db/database.go
# - Add Queries struct
# - Add prepareQueries() function
# - Add closeQueries() function
# - Add getDefaultContext() helper
# - Update SetupDatabase()
# - Update Close()

# Verify builds
go build .
```

**Step 2: Update Database Functions**

```bash
# Update all 27 functions to use prepared statements
# Files: db/database.go

# Pattern for each function:
# 1. Create context with getDefaultContext()
# 2. Use queries.StatementName.ExecContext/QueryContext/GetContext
# 3. Defer cancel()
# 4. Handle errors

# Verify builds
go build .
```

**Step 3: Test Locally**

```bash
# Start server
./hdd &
PID=$!

# Check logs for successful preparation
# Expected: "Successfully prepared all database queries statement_count=27"

# Run profiling tests
./test_prepared_statements.sh

# Check PostgreSQL
psql -U hddb -d hdd_db -c "SELECT count(*) FROM pg_prepared_statements;"
# Expected: 27

# Graceful shutdown
kill -TERM $PID

# Expected logs:
# INFO Closing prepared statements...
# INFO Closed prepared statements closed=27 total=27
# INFO Database connection closed
```

**Step 4: Run Benchmarks**

```bash
# Run performance benchmarks
go test -bench=. -benchmem ./db/

# Expected:
# BenchmarkGetScanById-8   500000   2500 ns/op   (vs 7000 ns/op before)
# Significant improvement in query-heavy operations
```

**Step 5: Deploy to Staging**

```bash
# Build
go build -o hdd

# Deploy
scp hdd staging:/opt/bhandaar/
ssh staging 'systemctl restart bhandaar'

# Verify logs
ssh staging 'journalctl -u bhandaar -n 50'

# Expected:
# - "Successfully prepared all database queries"
# - No errors about prepared statement failures
```

**Step 6: Monitor Staging**

```bash
# Watch for preparation errors
ssh staging 'journalctl -u bhandaar -f | grep -E "prepare|statement"'

# Check PostgreSQL prepared statements
ssh staging 'docker exec postgres psql -U hddb -d hdd_db -c "SELECT count(*) FROM pg_prepared_statements;"'

# Monitor performance
# Run load tests against staging
vegeta attack -targets=staging_targets.txt -rate=50/s -duration=60s | vegeta report

# Expected:
# - Lower latency than before
# - Same success rate (100%)
# - Stable memory usage
```

**Step 7: Deploy to Production**

```bash
# Tag release
git tag -a v1.x.x -m "Add prepared statements for all database queries (Issue #18)"
git push origin v1.x.x

# Build production binary
go build -o hdd

# Deploy (Kubernetes example)
docker build -t jyothri/hdd-go-build:v1.x.x .
docker push jyothri/hdd-go-build:v1.x.x

kubectl set image deployment/bhandaar-backend backend=jyothri/hdd-go-build:v1.x.x
kubectl rollout status deployment/bhandaar-backend
```

**Step 8: Post-Deployment Validation**

```bash
# Check logs
kubectl logs -f deployment/bhandaar-backend | grep "prepared"

# Expected:
# INFO Successfully prepared all database queries statement_count=27

# Monitor performance
kubectl exec -it <pod> -- curl localhost:8090/api/scans/1
# Should be faster than before

# Check PostgreSQL
kubectl exec -it <postgres-pod> -- psql -U hddb -d hdd_db -c "SELECT count(*) FROM pg_prepared_statements;"
# Expected: 27
```

### 5.3 Rollback Plan

**If issues detected:**

```bash
# Kubernetes
kubectl rollout undo deployment/bhandaar-backend

# systemd
ssh production 'systemctl stop bhandaar'
ssh production 'cp /opt/bhandaar/hdd.backup /opt/bhandaar/hdd'
ssh production 'systemctl start bhandaar'

# Verify
curl https://api.production.com/api/health
```

### 5.4 Monitoring Post-Deployment

**Metrics to Watch:**

1. **Query Performance** (from application logs or APM)
   ```bash
   # Average query time should decrease
   # Before: 7-10ms
   # After: 2-5ms
   ```

2. **Database CPU** (from PostgreSQL monitoring)
   ```bash
   # PostgreSQL CPU usage should decrease
   # Less time spent parsing queries
   ```

3. **Prepared Statement Count**
   ```sql
   SELECT count(*) FROM pg_prepared_statements;
   -- Expected: 27 per connection
   ```

4. **Application Memory**
   ```bash
   # Should increase by ~2-5 MB (acceptable)
   kubectl top pod -l app=bhandaar-backend
   ```

5. **Error Rates**
   ```bash
   # Should remain the same (no increase)
   kubectl logs -f deployment/bhandaar-backend | grep ERROR
   ```

---

## 6. Integration with Issue #17

### 6.1 Context Timeout Usage

**Issue #17 provides:**
- getDefaultContext() helper for 30-second query timeout
- Connection pool configuration
- Database health checks

**Issue #18 uses:**
- All prepared statements use getDefaultContext() for timeout
- Pattern: ctx, cancel := getDefaultContext(); defer cancel()
- Prevents queries from running indefinitely

**Combined example:**

```go
func GetScanById(scanId int) (*Scan, error) {
	// Use context from Issue #17
	ctx, cancel := getDefaultContext()  // 30-second timeout
	defer cancel()

	// Use prepared statement from Issue #18
	var scan Scan
	err := queries.selectScanById.GetContext(ctx, &scan, scanId)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("query timeout after 30s for scan %d: %w", scanId, err)
		}
		return nil, fmt.Errorf("failed to get scan %d: %w", scanId, err)
	}

	return &scan, nil
}
```

### 6.2 Shutdown Sequence

**Combined shutdown from Issue #17 + #18:**

```go
// In db/database.go Close() function:
func Close() error {
	slog.Info("Closing database resources...")

	// 1. Close prepared statements first (Issue #18)
	if err := closeQueries(); err != nil {
		slog.Error("Error closing prepared statements", "error", err)
		// Continue to close database anyway
	}

	// 2. Close database connection (Issue #17)
	if db != nil {
		if err := db.Close(); err != nil {
			return fmt.Errorf("failed to close database: %w", err)
		}
		slog.Info("Database connection closed")
	}

	return nil
}
```

**Shutdown logs:**

```
INFO Shutting down server...
INFO Closing database resources...
INFO Closing prepared statements...
INFO Closed prepared statements closed=27 total=27
INFO Database connection closed
INFO Server exited
```

---

## Appendix A: Complete Function Update Checklist

All database functions that need updating:

**Scan Management (6 functions):**
- [x] LogStartScan - Use queries.insertScan
- [x] MarkScanCompleted - Use queries.updateScanComplete
- [x] MarkScanFailed - Use queries.updateScanFailed
- [x] GetScanById - Use queries.selectScanById
- [x] GetScansFromDb - Use queries.selectScans + queries.countScans
- [x] DeleteScan - Use 7 delete statements (transaction-aware)

**Scan Metadata (2 functions):**
- [x] SaveScanMetadata - Use queries.insertScanMetadata
- [x] GetScanRequestsFromDb - Use queries.selectScanRequests

**Scan Data (2 functions):**
- [x] SaveStatToDb - Use queries.insertScanData (loop)
- [x] GetScanDataFromDb - Use queries.selectScanData + queries.countScanData

**Message Metadata (2 functions):**
- [x] SaveMessageMetadataToDb - Use queries.checkDuplicateMessage + queries.insertMessageMetadata (loop)
- [x] GetMessageMetadataFromDb - Use queries.selectMessageMetadata + queries.countMessageMetadata

**Photos Media Item (2 functions):**
- [x] SavePhotosMediaItemToDb - Use queries.insertPhotosMediaItem + queries.insertPhotoMetadata + queries.insertVideoMetadata (transaction-aware loop)
- [x] GetPhotosMediaItemFromDb - Use queries.selectPhotosMediaItem + queries.countPhotosMediaItem

**OAuth Tokens (2 functions):**
- [x] SaveOAuthToken - Use queries.insertOAuthToken
- [x] GetOAuthToken - Use queries.selectOAuthToken

**Accounts (2 functions):**
- [x] GetRequestAccountsFromDb - Use queries.selectRequestAccounts
- [x] GetAccountsFromDb - Use queries.selectAccounts

**Total: 18 functions updated**

**Not updated (migration functions, run only once):**
- migrateDB - Uses inline SQL (acceptable, runs once)
- migrateDBv0 - Uses inline SQL (acceptable, runs once)
- migrateAddStatusColumn - Uses inline SQL (acceptable, runs once)

---

## Appendix B: Performance Comparison

### Expected Performance Improvements

| Operation | Before (ms) | After (ms) | Improvement |
|-----------|-------------|------------|-------------|
| LogStartScan | 7 | 2 | 71% faster |
| GetScanById | 5 | 2 | 60% faster |
| InsertScanData (1000x) | 7000 | 2000 | 71% faster |
| InsertMessageMetadata (1000x) | 7000 | 2000 | 71% faster |
| GetScansFromDb | 10 | 4 | 60% faster |
| GetMessageMetadataFromDb | 8 | 3 | 62% faster |

### Database CPU Reduction

```
Before:
- Parse query: 70% of query time
- Execute query: 30% of query time

After:
- Parse query: 0% (done once at startup)
- Execute query: 100% of query time
```

### Throughput Improvement

```
Concurrent scan inserts:
Before: ~140 inserts/second
After: ~500 inserts/second
Improvement: 3.5x throughput
```

---

## Appendix C: Troubleshooting Guide

### Problem: Startup fails with "failed to prepare queries"

**Diagnosis:**
```bash
# Check logs for specific statement error
grep "failed to prepare" /var/log/bhandaar/app.log

# Example:
# ERROR failed to prepare insertScan: syntax error at or near "INSRT"
```

**Solution:**
- Review SQL syntax in prepareQueries()
- Ensure all placeholders ($1, $2) are correct
- Verify table/column names match schema

### Problem: "statement is closed" error during runtime

**Diagnosis:**
```bash
# Check logs
grep "statement is closed" /var/log/bhandaar/app.log
```

**Causes:**
1. Prepared statement closed prematurely
2. Using statement after database close
3. Double-close of statement

**Solution:**
- Ensure closeQueries() only called during shutdown
- Don't close individual statements manually
- Check for race conditions in shutdown sequence

### Problem: Transaction fails with prepared statement

**Diagnosis:**
```bash
# Check logs for transaction errors
grep "transaction" /var/log/bhandaar/app.log | grep -i error
```

**Common Cause:**
- Using db-scoped statement in transaction instead of tx.Stmt()

**Solution:**
```go
// Wrong:
result, err := queries.insertScanData.ExecContext(ctx, ...)

// Right:
txStmt := tx.Stmtx(queries.insertScanData)
result, err := txStmt.ExecContext(ctx, ...)
```

### Problem: Performance not improved

**Diagnosis:**
```bash
# Check if prepared statements actually being used
psql -U hddb -d hdd_db -c "SELECT count(*) FROM pg_prepared_statements;"
# Expected: 27

# If 0, statements not being prepared
```

**Solution:**
- Verify prepareQueries() called in SetupDatabase()
- Check logs for preparation errors
- Ensure functions using queries.StatementName not inline SQL

### Problem: Memory usage increased significantly

**Diagnosis:**
```bash
# Check memory usage
ps aux | grep hdd | awk '{print $6}'

# Expected increase: 2-5 MB
# If > 50 MB increase, possible leak
```

**Solution:**
- Verify closeQueries() called on shutdown
- Check for statement leaks in transaction code
- Use pprof to profile memory usage

---

**END OF DOCUMENT**
