# Issue #9 Implementation Plan: Hardcoded Database Credentials

**Document Version:** 1.0
**Created:** 2025-12-21
**Status:** Planning Phase
**Priority:** P1 - High Priority (Security & Configuration)

---

## Executive Summary

This document provides a comprehensive implementation plan to address **Issue #9: Hardcoded Database Credentials**. The current system has database credentials hardcoded in source code, creating security risks and deployment inflexibility.

**Selected Approach:**
- **Configuration Method**: Environment variables only (12-factor app)
- **Default Values**: No default for password (security-first)
- **SSL/TLS**: Simple SSL mode flag (disable/require/verify-ca/verify-full)
- **Connection Pool**: Configurable with good defaults
- **Validation**: At startup - fail fast if invalid
- **Backwards Compatibility**: Breaking change - remove hardcoded values

**Estimated Effort:** 3-4 hours

**Impact:**
- Eliminates credentials from source code
- Enables different configs for dev/staging/prod
- Improves security posture
- Supports containerized deployments
- Enables connection pool tuning

---

## Table of Contents

1. [Current State Analysis](#1-current-state-analysis)
2. [Target Architecture](#2-target-architecture)
3. [Implementation Details](#3-implementation-details)
4. [Migration Strategy](#4-migration-strategy)
5. [Testing Strategy](#5-testing-strategy)
6. [Deployment Plan](#6-deployment-plan)
7. [Security Considerations](#7-security-considerations)

---

## 1. Current State Analysis

### 1.1 Current Implementation

**db/database.go (lines 13-19):**
```go
const (
    host     = "hdd_db"      // ❌ Hardcoded
    port     = 5432          // ❌ Hardcoded
    user     = "hddb"        // ❌ Hardcoded
    password = "hddb"        // ❌ CRITICAL: Password in source!
    dbname   = "hdd_db"      // ❌ Hardcoded
)
```

**db/database.go (lines 24-27):**
```go
func SetupDatabase() error {
    psqlInfo := fmt.Sprintf("host=%s port=%d user=%s "+
        "password=%s dbname=%s sslmode=disable",
        host, port, user, password, dbname)
    // ❌ No SSL/TLS support
    // ❌ No connection pool configuration
}
```

### 1.2 Security Vulnerabilities

| Vulnerability | Severity | Impact |
|---------------|----------|---------|
| **Password in source code** | CRITICAL | Anyone with repo access has DB credentials |
| **Password in git history** | CRITICAL | Changing password doesn't help - still in history |
| **Same credentials everywhere** | HIGH | Can't use different passwords for dev/prod |
| **No SSL enforcement** | HIGH | Credentials transmitted in plaintext over network |
| **No credential rotation** | MEDIUM | Changing password requires code change + deployment |

### 1.3 Operational Issues

| Issue | Impact | Severity |
|-------|--------|----------|
| **Can't configure per environment** | Must rebuild for different envs | HIGH |
| **No connection pool tuning** | Can't optimize for workload | MEDIUM |
| **Docker deployments difficult** | Can't pass config via env vars | HIGH |
| **Kubernetes secrets unusable** | Can't inject secrets | HIGH |
| **CI/CD complications** | Can't test with different DBs | MEDIUM |

### 1.4 Current OAuth Configuration (for comparison)

**constants/constants.go:**
```go
// OAuth uses command-line flags
var (
    OauthClientId     string
    OauthClientSecret string
    FrontendUrl       string
)

func init() {
    flag.StringVar(&OauthClientId, "oauth_client_id", "dummy", "oauth client id")
    flag.StringVar(&OauthClientSecret, "oauth_client_secret", "dummy", "oauth client secret")
    flag.StringVar(&FrontendUrl, "frontend_url", "http://localhost:5173", "URLs allowlisted by UI for CORS.")
    flag.Parse()
}
```

**Note:** OAuth uses flags, but environment variables are more cloud-native and container-friendly.

### 1.5 Risk Assessment

**Without proper configuration management:**
- 🔴 **Critical**: Database password compromised if repo is leaked
- 🔴 **Critical**: Cannot use different passwords for prod vs dev
- 🔴 **High**: Git history contains all old passwords forever
- 🟡 **Medium**: Cannot enforce SSL/TLS connections
- 🟡 **Medium**: Cannot tune connection pool for production workload

**Attack Scenarios:**
1. **Public repo leak**: Password "hddb" exposed to internet
2. **Former employee**: Still has credentials from git history
3. **Developer laptop compromise**: Source code contains production credentials
4. **Supply chain attack**: Dependencies with malware can steal credentials

---

## 2. Target Architecture

### 2.1 Configuration Structure

```
Environment Variables (Required)
├── DB_HOST (default: localhost)
├── DB_PORT (default: 5432)
├── DB_USER (default: hddb)
├── DB_PASSWORD (REQUIRED - no default)
├── DB_NAME (default: hdd_db)
├── DB_SSLMODE (default: disable)
│
Connection Pool (Optional with defaults)
├── DB_MAX_OPEN_CONNS (default: 25)
├── DB_MAX_IDLE_CONNS (default: 5)
├── DB_CONN_MAX_LIFETIME (default: 5m)
└── DB_CONN_MAX_IDLE_TIME (default: 10m)
```

### 2.2 Configuration Flow

```
┌─────────────────────────────────────────────────────────────┐
│ 1. Application Starts                                       │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 2. Load Configuration from Environment Variables           │
│    - ReadDBConfigFromEnv()                                  │
│    - Falls back to defaults (except password)               │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 3. Validate Configuration                                   │
│    - Check DB_PASSWORD is set (REQUIRED)                   │
│    - Validate port range (1-65535)                         │
│    - Validate SSL mode (disable/require/verify-ca/verify-full) │
│    - Validate connection pool settings                      │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
                    ┌───────────────┐
                    │  Valid?       │
                    └───────────────┘
                      │           │
                     Yes          No
                      │           │
                      │           ▼
                      │     ┌─────────────────────────────┐
                      │     │ Log Error with Details      │
                      │     │ Exit with code 1            │
                      │     └─────────────────────────────┘
                      │
                      ▼
┌─────────────────────────────────────────────────────────────┐
│ 4. Build Connection String                                  │
│    - Construct PostgreSQL DSN                               │
│    - Include SSL mode                                       │
│    - Log config (REDACTED password)                        │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 5. Open Database Connection                                 │
│    - sqlx.Open()                                            │
│    - Set connection pool parameters                         │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 6. Verify Connection                                        │
│    - db.Ping()                                              │
│    - Log success or fail                                    │
└─────────────────────────────────────────────────────────────┘
                            │
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 7. Run Migrations                                           │
│    - migrateDB()                                            │
└─────────────────────────────────────────────────────────────┘
```

### 2.3 Environment Variables Reference

| Variable | Required | Default | Example | Description |
|----------|----------|---------|---------|-------------|
| `DB_HOST` | No | `localhost` | `postgres.example.com` | Database server hostname |
| `DB_PORT` | No | `5432` | `5432` | Database server port |
| `DB_USER` | No | `hddb` | `app_user` | Database username |
| `DB_PASSWORD` | **YES** | *(none)* | `SecureP@ssw0rd` | Database password |
| `DB_NAME` | No | `hdd_db` | `bhandaar_prod` | Database name |
| `DB_SSLMODE` | No | `disable` | `require` | SSL mode (disable/require/verify-ca/verify-full) |
| `DB_MAX_OPEN_CONNS` | No | `25` | `50` | Maximum open connections |
| `DB_MAX_IDLE_CONNS` | No | `5` | `10` | Maximum idle connections |
| `DB_CONN_MAX_LIFETIME` | No | `5m` | `10m` | Connection max lifetime |
| `DB_CONN_MAX_IDLE_TIME` | No | `10m` | `15m` | Connection max idle time |

---

## 3. Implementation Details

### 3.1 Database Configuration: `db/config.go` (NEW FILE)

```go
package db

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// DBConfig holds database configuration
type DBConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	DBName   string
	SSLMode  string

	// Connection pool settings
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// LoadDBConfig loads database configuration from environment variables
func LoadDBConfig() (*DBConfig, error) {
	config := &DBConfig{
		Host:            getEnv("DB_HOST", "localhost"),
		Port:            getEnvInt("DB_PORT", 5432),
		User:            getEnv("DB_USER", "hddb"),
		Password:        getEnv("DB_PASSWORD", ""),
		DBName:          getEnv("DB_NAME", "hdd_db"),
		SSLMode:         getEnv("DB_SSLMODE", "disable"),
		MaxOpenConns:    getEnvInt("DB_MAX_OPEN_CONNS", 25),
		MaxIdleConns:    getEnvInt("DB_MAX_IDLE_CONNS", 5),
		ConnMaxLifetime: getEnvDuration("DB_CONN_MAX_LIFETIME", 5*time.Minute),
		ConnMaxIdleTime: getEnvDuration("DB_CONN_MAX_IDLE_TIME", 10*time.Minute),
	}

	if err := config.Validate(); err != nil {
		return nil, err
	}

	return config, nil
}

// Validate validates the database configuration
func (c *DBConfig) Validate() error {
	// Password is required (security-first approach)
	if c.Password == "" {
		return errors.New("DB_PASSWORD environment variable is required")
	}

	// Validate port range
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("invalid DB_PORT: %d (must be 1-65535)", c.Port)
	}

	// Validate SSL mode
	validSSLModes := map[string]bool{
		"disable":     true,
		"require":     true,
		"verify-ca":   true,
		"verify-full": true,
	}
	if !validSSLModes[c.SSLMode] {
		return fmt.Errorf("invalid DB_SSLMODE: %s (must be disable/require/verify-ca/verify-full)",
			c.SSLMode)
	}

	// Validate connection pool settings
	if c.MaxOpenConns < 1 {
		return fmt.Errorf("invalid DB_MAX_OPEN_CONNS: %d (must be >= 1)", c.MaxOpenConns)
	}

	if c.MaxIdleConns < 0 {
		return fmt.Errorf("invalid DB_MAX_IDLE_CONNS: %d (must be >= 0)", c.MaxIdleConns)
	}

	if c.MaxIdleConns > c.MaxOpenConns {
		return fmt.Errorf("DB_MAX_IDLE_CONNS (%d) cannot be greater than DB_MAX_OPEN_CONNS (%d)",
			c.MaxIdleConns, c.MaxOpenConns)
	}

	if c.ConnMaxLifetime < 0 {
		return fmt.Errorf("invalid DB_CONN_MAX_LIFETIME: %s (must be >= 0)", c.ConnMaxLifetime)
	}

	if c.ConnMaxIdleTime < 0 {
		return fmt.Errorf("invalid DB_CONN_MAX_IDLE_TIME: %s (must be >= 0)", c.ConnMaxIdleTime)
	}

	return nil
}

// ConnectionString builds a PostgreSQL connection string
func (c *DBConfig) ConnectionString() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		c.Host, c.Port, c.User, c.Password, c.DBName, c.SSLMode,
	)
}

// LogSafeConfig returns a config string safe for logging (password redacted)
func (c *DBConfig) LogSafeConfig() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s dbname=%s sslmode=%s max_open=%d max_idle=%d",
		c.Host, c.Port, c.User, c.DBName, c.SSLMode,
		c.MaxOpenConns, c.MaxIdleConns,
	)
}

// Helper functions

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}
	return defaultValue
}

func getEnvDuration(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			return d
		}
	}
	return defaultValue
}
```

### 3.2 Database Setup Update: `db/database.go`

**Remove hardcoded constants and update SetupDatabase():**

```go
package db

import (
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
)

// Remove these hardcoded constants:
// const (
//     host     = "hdd_db"
//     port     = 5432
//     user     = "hddb"
//     password = "hddb"
//     dbname   = "hdd_db"
// )

var db *sqlx.DB

// SetupDatabase initializes the database connection and runs migrations
func SetupDatabase() error {
	// Load configuration from environment variables
	config, err := LoadDBConfig()
	if err != nil {
		return fmt.Errorf("failed to load database configuration: %w", err)
	}

	// Log configuration (with password redacted)
	slog.Info("Database configuration loaded", "config", config.LogSafeConfig())

	// Open database connection
	connStr := config.ConnectionString()
	db, err = sqlx.Open("postgres", connStr)
	if err != nil {
		return fmt.Errorf("failed to open database connection: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(config.MaxOpenConns)
	db.SetMaxIdleConns(config.MaxIdleConns)
	db.SetConnMaxLifetime(config.ConnMaxLifetime)
	db.SetConnMaxIdleTime(config.ConnMaxIdleTime)

	slog.Info("Database connection pool configured",
		"max_open_conns", config.MaxOpenConns,
		"max_idle_conns", config.MaxIdleConns,
		"conn_max_lifetime", config.ConnMaxLifetime,
		"conn_max_idle_time", config.ConnMaxIdleTime)

	// Test connection
	err = db.Ping()
	if err != nil {
		return fmt.Errorf("failed to ping database: %w", err)
	}

	slog.Info("Successfully connected to database")

	// Run migrations
	if err := migrateDB(); err != nil {
		return fmt.Errorf("failed to run database migrations: %w", err)
	}

	return nil
}

// Close closes the database connection
func Close() error {
	if db != nil {
		return db.Close()
	}
	return nil
}

// ... rest of database.go unchanged
```

### 3.3 Example Environment Configuration

**`.env.example`** (NEW FILE - for documentation):

```bash
# Database Configuration
# Required: DB_PASSWORD must be set
# Optional: All others have defaults

# Database connection settings
DB_HOST=localhost
DB_PORT=5432
DB_USER=hddb
DB_PASSWORD=your_secure_password_here
DB_NAME=hdd_db
DB_SSLMODE=disable

# Connection pool settings (optional - good defaults provided)
DB_MAX_OPEN_CONNS=25
DB_MAX_IDLE_CONNS=5
DB_CONN_MAX_LIFETIME=5m
DB_CONN_MAX_IDLE_TIME=10m

# OAuth Configuration (existing - via command-line flags)
# Pass these as flags: -oauth_client_id=... -oauth_client_secret=...
# OAUTH_CLIENT_ID=your_google_client_id
# OAUTH_CLIENT_SECRET=your_google_client_secret
# FRONTEND_URL=http://localhost:5173
```

**`.env.development`** (NEW FILE - for local development):

```bash
# Development environment configuration
DB_HOST=localhost
DB_PORT=5432
DB_USER=hddb
DB_PASSWORD=hddb_dev_password
DB_NAME=hdd_db
DB_SSLMODE=disable

# Development uses smaller pool
DB_MAX_OPEN_CONNS=10
DB_MAX_IDLE_CONNS=2
```

**`.env.production`** (NEW FILE - template, not committed):

```bash
# Production environment configuration
# DO NOT COMMIT THIS FILE TO GIT

DB_HOST=prod-postgres.example.com
DB_PORT=5432
DB_USER=bhandaar_prod
DB_PASSWORD=REPLACE_WITH_ACTUAL_PRODUCTION_PASSWORD
DB_NAME=bhandaar_prod
DB_SSLMODE=verify-full

# Production uses larger pool
DB_MAX_OPEN_CONNS=50
DB_MAX_IDLE_CONNS=10
DB_CONN_MAX_LIFETIME=10m
DB_CONN_MAX_IDLE_TIME=15m
```

### 3.4 Update `.gitignore`

**Add to `.gitignore`:**

```gitignore
# Environment files with credentials
.env
.env.local
.env.production
.env.staging

# Keep example files
!.env.example
!.env.development
```

---

## 4. Migration Strategy

### 4.1 Migration Path

**Breaking Change Notice:**

This is a **breaking change** - applications will not start without `DB_PASSWORD` environment variable set.

**Migration Steps:**

1. **Update code** (implement config.go and update database.go)
2. **Set environment variables** in deployment environment
3. **Deploy new version**
4. **Remove old hardcoded values from git history** (optional but recommended)

### 4.2 Deployment Environments

**Local Development:**

```bash
# Option 1: Export in shell
export DB_PASSWORD=hddb
go run .

# Option 2: Use .env file with direnv
# Install direnv: brew install direnv
# Create .env file
echo "DB_PASSWORD=hddb" > .env
direnv allow
go run .

# Option 3: Inline
DB_PASSWORD=hddb go run .
```

**Docker:**

```dockerfile
# Dockerfile (unchanged, but now expects env vars)
FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o hdd

FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/hdd .
CMD ["./hdd"]
```

```bash
# docker-compose.yml
version: '3.8'
services:
  backend:
    build: .
    environment:
      - DB_HOST=postgres
      - DB_PORT=5432
      - DB_USER=hddb
      - DB_PASSWORD=${DB_PASSWORD}  # From .env file or shell
      - DB_NAME=hdd_db
      - DB_SSLMODE=disable
    depends_on:
      - postgres

  postgres:
    image: postgres:15-alpine
    environment:
      - POSTGRES_USER=hddb
      - POSTGRES_PASSWORD=${DB_PASSWORD}
      - POSTGRES_DB=hdd_db
    ports:
      - "5432:5432"
```

**Kubernetes:**

```yaml
# secret.yaml
apiVersion: v1
kind: Secret
metadata:
  name: bhandaar-db-credentials
type: Opaque
stringData:
  password: your_production_password_here

---
# deployment.yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: bhandaar-backend
spec:
  template:
    spec:
      containers:
      - name: backend
        image: jyothri/hdd-go-build:latest
        env:
          - name: DB_HOST
            value: "postgres-service"
          - name: DB_PORT
            value: "5432"
          - name: DB_USER
            value: "hddb"
          - name: DB_PASSWORD
            valueFrom:
              secretKeyRef:
                name: bhandaar-db-credentials
                key: password
          - name: DB_NAME
            value: "hdd_db"
          - name: DB_SSLMODE
            value: "require"
          - name: DB_MAX_OPEN_CONNS
            value: "50"
          - name: DB_MAX_IDLE_CONNS
            value: "10"
```

**systemd:**

```ini
# /etc/systemd/system/bhandaar.service
[Unit]
Description=Bhandaar Storage Analyzer
After=network.target postgresql.service

[Service]
Type=simple
User=bhandaar
WorkingDirectory=/opt/bhandaar
ExecStart=/opt/bhandaar/hdd

# Environment variables
Environment="DB_HOST=localhost"
Environment="DB_PORT=5432"
Environment="DB_USER=hddb"
Environment="DB_NAME=hdd_db"
Environment="DB_SSLMODE=require"

# Load password from separate file (not in git)
EnvironmentFile=/etc/bhandaar/db-credentials.env

Restart=on-failure
RestartSec=5s

[Install]
WantedBy=multi-user.target
```

```bash
# /etc/bhandaar/db-credentials.env
# chmod 600, owned by bhandaar user
DB_PASSWORD=production_password_here
```

### 4.3 Removing Credentials from Git History

**IMPORTANT:** Old commits still contain the password "hddb" in git history.

**Option 1: BFG Repo-Cleaner (Recommended)**

```bash
# Install BFG
brew install bfg  # macOS
# or download from https://rtyley.github.io/bfg-repo-cleaner/

# Clone a fresh copy
git clone --mirror https://github.com/your-username/bhandaar.git
cd bhandaar.git

# Remove password from all history
bfg --replace-text <(echo 'password="hddb"==>password="REMOVED"')

# Clean up
git reflog expire --expire=now --all
git gc --prune=now --aggressive

# Force push (WARNING: Rewrites history)
git push --force
```

**Option 2: git filter-branch**

```bash
git filter-branch --tree-filter \
  'find . -name "database.go" -exec sed -i "s/password = \"hddb\"/password = \"REMOVED\"/g" {} \;' \
  HEAD

git push --force
```

**⚠️ WARNING:** Rewriting history affects all collaborators. Coordinate with team!

---

## 5. Testing Strategy

### 5.1 Unit Tests

**`db/config_test.go`** (NEW FILE)

```go
package db

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadDBConfig_AllEnvVarsSet(t *testing.T) {
	// Setup environment
	os.Setenv("DB_HOST", "testhost")
	os.Setenv("DB_PORT", "5433")
	os.Setenv("DB_USER", "testuser")
	os.Setenv("DB_PASSWORD", "testpass")
	os.Setenv("DB_NAME", "testdb")
	os.Setenv("DB_SSLMODE", "require")
	defer cleanupEnv()

	config, err := LoadDBConfig()

	require.NoError(t, err)
	assert.Equal(t, "testhost", config.Host)
	assert.Equal(t, 5433, config.Port)
	assert.Equal(t, "testuser", config.User)
	assert.Equal(t, "testpass", config.Password)
	assert.Equal(t, "testdb", config.DBName)
	assert.Equal(t, "require", config.SSLMode)
}

func TestLoadDBConfig_DefaultValues(t *testing.T) {
	// Only set required password
	os.Setenv("DB_PASSWORD", "testpass")
	defer cleanupEnv()

	config, err := LoadDBConfig()

	require.NoError(t, err)
	assert.Equal(t, "localhost", config.Host)
	assert.Equal(t, 5432, config.Port)
	assert.Equal(t, "hddb", config.User)
	assert.Equal(t, "testpass", config.Password)
	assert.Equal(t, "hdd_db", config.DBName)
	assert.Equal(t, "disable", config.SSLMode)
}

func TestLoadDBConfig_MissingPassword(t *testing.T) {
	cleanupEnv()

	_, err := LoadDBConfig()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "DB_PASSWORD")
}

func TestValidate_InvalidPort(t *testing.T) {
	config := &DBConfig{
		Host:     "localhost",
		Port:     99999, // Invalid
		User:     "hddb",
		Password: "test",
		DBName:   "test",
		SSLMode:  "disable",
	}

	err := config.Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid DB_PORT")
}

func TestValidate_InvalidSSLMode(t *testing.T) {
	config := &DBConfig{
		Host:     "localhost",
		Port:     5432,
		User:     "hddb",
		Password: "test",
		DBName:   "test",
		SSLMode:  "invalid-mode",
	}

	err := config.Validate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid DB_SSLMODE")
}

func TestValidate_ConnectionPoolSettings(t *testing.T) {
	tests := []struct {
		name          string
		maxOpen       int
		maxIdle       int
		expectedError string
	}{
		{
			name:          "idle greater than open",
			maxOpen:       10,
			maxIdle:       20,
			expectedError: "cannot be greater than",
		},
		{
			name:          "negative max open",
			maxOpen:       -1,
			maxIdle:       5,
			expectedError: "must be >= 1",
		},
		{
			name:          "negative max idle",
			maxOpen:       10,
			maxIdle:       -1,
			expectedError: "must be >= 0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &DBConfig{
				Host:         "localhost",
				Port:         5432,
				User:         "hddb",
				Password:     "test",
				DBName:       "test",
				SSLMode:      "disable",
				MaxOpenConns: tt.maxOpen,
				MaxIdleConns: tt.maxIdle,
			}

			err := config.Validate()

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.expectedError)
		})
	}
}

func TestConnectionString(t *testing.T) {
	config := &DBConfig{
		Host:     "testhost",
		Port:     5433,
		User:     "testuser",
		Password: "testpass",
		DBName:   "testdb",
		SSLMode:  "require",
	}

	connStr := config.ConnectionString()

	assert.Equal(t,
		"host=testhost port=5433 user=testuser password=testpass dbname=testdb sslmode=require",
		connStr)
}

func TestLogSafeConfig(t *testing.T) {
	config := &DBConfig{
		Host:         "testhost",
		Port:         5433,
		User:         "testuser",
		Password:     "secret_password",
		DBName:       "testdb",
		SSLMode:      "require",
		MaxOpenConns: 25,
		MaxIdleConns: 5,
	}

	logStr := config.LogSafeConfig()

	// Should not contain password
	assert.NotContains(t, logStr, "secret_password")
	assert.NotContains(t, logStr, "password")

	// Should contain other info
	assert.Contains(t, logStr, "testhost")
	assert.Contains(t, logStr, "5433")
	assert.Contains(t, logStr, "testuser")
}

func TestGetEnvDuration(t *testing.T) {
	os.Setenv("TEST_DURATION", "15m")
	defer os.Unsetenv("TEST_DURATION")

	duration := getEnvDuration("TEST_DURATION", 5*time.Minute)

	assert.Equal(t, 15*time.Minute, duration)
}

func cleanupEnv() {
	os.Unsetenv("DB_HOST")
	os.Unsetenv("DB_PORT")
	os.Unsetenv("DB_USER")
	os.Unsetenv("DB_PASSWORD")
	os.Unsetenv("DB_NAME")
	os.Unsetenv("DB_SSLMODE")
	os.Unsetenv("DB_MAX_OPEN_CONNS")
	os.Unsetenv("DB_MAX_IDLE_CONNS")
	os.Unsetenv("DB_CONN_MAX_LIFETIME")
	os.Unsetenv("DB_CONN_MAX_IDLE_TIME")
}
```

### 5.2 Integration Tests

```bash
# Test with different configurations

# Test 1: Minimal config (password only)
DB_PASSWORD=test go test ./db/...

# Test 2: Full config
DB_HOST=localhost \
DB_PORT=5432 \
DB_USER=hddb \
DB_PASSWORD=test \
DB_NAME=hdd_db \
DB_SSLMODE=disable \
go test ./db/...

# Test 3: Missing password (should fail)
go test ./db/...
# Expected: Configuration error

# Test 4: Invalid port
DB_PASSWORD=test DB_PORT=99999 go test ./db/...
# Expected: Validation error
```

### 5.3 Manual Testing

```bash
# Test 1: Start with env vars
export DB_PASSWORD=hddb
go run .
# Expected: "Successfully connected to database"

# Test 2: Missing password
unset DB_PASSWORD
go run .
# Expected: "DB_PASSWORD environment variable is required"
# Exit code: 1

# Test 3: Custom configuration
DB_HOST=postgres.example.com \
DB_PORT=5432 \
DB_USER=myuser \
DB_PASSWORD=mypass \
DB_NAME=mydb \
DB_SSLMODE=require \
go run .

# Test 4: Connection pool configuration
DB_PASSWORD=hddb \
DB_MAX_OPEN_CONNS=50 \
DB_MAX_IDLE_CONNS=10 \
go run .
# Check logs for: "max_open_conns=50 max_idle_conns=10"
```

---

## 6. Deployment Plan

### 6.1 Pre-Deployment Checklist

- [ ] Code review completed
- [ ] Unit tests passing
- [ ] Integration tests passing
- [ ] `.env.example` created and documented
- [ ] `.gitignore` updated
- [ ] Deployment documentation updated
- [ ] Team trained on new configuration method
- [ ] Secrets configured in deployment environment

### 6.2 Deployment Steps

**Step 1: Prepare Secrets**

```bash
# For Kubernetes
kubectl create secret generic bhandaar-db-credentials \
  --from-literal=password='your_production_password'

# For systemd
sudo mkdir -p /etc/bhandaar
sudo bash -c 'echo "DB_PASSWORD=production_password" > /etc/bhandaar/db-credentials.env'
sudo chmod 600 /etc/bhandaar/db-credentials.env
sudo chown bhandaar:bhandaar /etc/bhandaar/db-credentials.env

# For Docker
echo "DB_PASSWORD=production_password" > .env
chmod 600 .env
```

**Step 2: Update Deployment Configuration**

```bash
# Update docker-compose.yml, k8s manifests, or systemd service files
# (See examples in Migration Strategy section)
```

**Step 3: Deploy to Staging**

```bash
# Build
cd be
go build -o hdd

# Deploy to staging with environment variables
ssh staging-server 'echo "DB_PASSWORD=staging_password" > /etc/bhandaar/db-credentials.env'
scp hdd staging-server:/opt/bhandaar/
ssh staging-server 'systemctl restart bhandaar'

# Verify
ssh staging-server 'journalctl -u bhandaar -n 50'
# Look for: "Database configuration loaded"
# Look for: "Successfully connected to database"
```

**Step 4: Verify Staging**

```bash
# Test database connection
curl https://staging-api.example.com/api/health
# Expected: {"ok":true}

# Check logs for password NOT being logged
ssh staging-server 'journalctl -u bhandaar | grep -i password'
# Should only see: "DB_PASSWORD environment variable is required" (if tested without it)
# Should NOT see actual password value
```

**Step 5: Deploy to Production**

```bash
# Set production password in secrets management system
kubectl create secret generic bhandaar-db-credentials \
  --from-literal=password='STRONG_PRODUCTION_PASSWORD_HERE' \
  --namespace=production

# Deploy new version
kubectl apply -f k8s/deployment.yaml --namespace=production

# Monitor rollout
kubectl rollout status deployment/bhandaar-backend --namespace=production

# Verify
kubectl logs -f deployment/bhandaar-backend --namespace=production
```

**Step 6: Post-Deployment Verification**

```bash
# Verify application started
curl https://api.production.com/api/health

# Check database connection in logs
kubectl logs deployment/bhandaar-backend | grep "Database"
# Expected: "Database configuration loaded"
# Expected: "Successfully connected to database"

# Verify password is not in logs
kubectl logs deployment/bhandaar-backend | grep -i password
# Should NOT find password value

# Test functionality
curl https://api.production.com/api/scans
```

### 6.3 Rollback Procedure

If issues occur:

```bash
# Kubernetes: Rollback to previous version
kubectl rollout undo deployment/bhandaar-backend

# systemd: Restore old binary
sudo systemctl stop bhandaar
sudo cp /opt/bhandaar/hdd.backup /opt/bhandaar/hdd
sudo systemctl start bhandaar

# Docker Compose: Use old image
docker-compose down
docker-compose up -d --force-recreate
```

**Note:** Rollback will restore hardcoded credentials! Plan migration carefully.

---

## 7. Security Considerations

### 7.1 Password Management Best Practices

**DO:**
- ✅ Use strong, unique passwords for each environment
- ✅ Store passwords in secrets management (Kubernetes Secrets, AWS Secrets Manager, HashiCorp Vault)
- ✅ Rotate passwords regularly
- ✅ Use different passwords for dev/staging/prod
- ✅ Restrict file permissions on credential files (chmod 600)
- ✅ Use SSL/TLS in production (DB_SSLMODE=require or verify-full)

**DON'T:**
- ❌ Commit .env files with real passwords to git
- ❌ Share production passwords via email/Slack
- ❌ Use the same password across environments
- ❌ Log passwords (even in error messages)
- ❌ Include passwords in URLs or command-line args (visible in process list)

### 7.2 SSL/TLS Configuration

**Production should use `verify-full`:**

```bash
DB_SSLMODE=verify-full
DB_SSLROOTCERT=/path/to/ca-cert.pem
DB_SSLCERT=/path/to/client-cert.pem
DB_SSLKEY=/path/to/client-key.pem
```

**Note:** Current implementation only supports sslmode. For full SSL with certs, extend DBConfig:

```go
type DBConfig struct {
    // ... existing fields
    SSLRootCert string
    SSLCert     string
    SSLKey      string
}

func (c *DBConfig) ConnectionString() string {
    connStr := fmt.Sprintf(
        "host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
        c.Host, c.Port, c.User, c.Password, c.DBName, c.SSLMode,
    )

    if c.SSLRootCert != "" {
        connStr += fmt.Sprintf(" sslrootcert=%s", c.SSLRootCert)
    }
    if c.SSLCert != "" {
        connStr += fmt.Sprintf(" sslcert=%s", c.SSLCert)
    }
    if c.SSLKey != "" {
        connStr += fmt.Sprintf(" sslkey=%s", c.SSLKey)
    }

    return connStr
}
```

### 7.3 Secrets Management Recommendations

**Development:**
- Use `.env` file (gitignored)
- Or use direnv for automatic environment loading

**Staging/Production:**

**Option 1: Kubernetes Secrets**
```bash
kubectl create secret generic bhandaar-db \
  --from-literal=password='...' \
  --from-literal=user='...'
```

**Option 2: AWS Secrets Manager**
```bash
aws secretsmanager create-secret \
  --name bhandaar/db/password \
  --secret-string 'your-password'
```

**Option 3: HashiCorp Vault**
```bash
vault kv put secret/bhandaar/db \
  password='your-password' \
  user='db-user'
```

**Option 4: Environment file with restricted permissions**
```bash
sudo bash -c 'cat > /etc/bhandaar/secrets.env << EOF
DB_PASSWORD=secure_password
EOF'
sudo chmod 400 /etc/bhandaar/secrets.env
sudo chown bhandaar:bhandaar /etc/bhandaar/secrets.env
```

### 7.4 Audit and Compliance

**Log Safe Configuration:**
```go
slog.Info("Database configuration loaded", "config", config.LogSafeConfig())
// Outputs: host=localhost port=5432 user=hddb dbname=hdd_db sslmode=disable
// Does NOT output password
```

**Monitor for Exposed Credentials:**
```bash
# Check logs don't contain password
grep -r "password.*=" /var/log/bhandaar/
# Should return nothing

# Check process list doesn't show password
ps aux | grep hdd
# Should not show DB_PASSWORD value
```

---

## Appendix A: Complete File Changes Summary

### Files to Create

1. **`db/config.go`** - NEW
   - DBConfig struct
   - LoadDBConfig() function
   - Validate() method
   - Helper functions (getEnv, getEnvInt, getEnvDuration)

2. **`db/config_test.go`** - NEW
   - Unit tests for configuration loading
   - Validation tests
   - Environment variable parsing tests

3. **`.env.example`** - NEW
   - Example configuration file
   - Documents all environment variables

4. **`.env.development`** - NEW
   - Development configuration
   - Safe to commit (no real credentials)

### Files to Modify

1. **`db/database.go`**
   - Remove hardcoded constants (lines 13-19)
   - Update SetupDatabase() to use LoadDBConfig()
   - Configure connection pool settings

2. **`.gitignore`**
   - Add .env patterns
   - Exclude production/staging env files

### Files to Delete (Optional)

- None (clean removal of hardcoded values)

---

## Appendix B: Environment Variables Quick Reference

```bash
# Minimal configuration (only required field)
DB_PASSWORD=your_password

# Full configuration (all options)
DB_HOST=localhost
DB_PORT=5432
DB_USER=hddb
DB_PASSWORD=your_password
DB_NAME=hdd_db
DB_SSLMODE=disable
DB_MAX_OPEN_CONNS=25
DB_MAX_IDLE_CONNS=5
DB_CONN_MAX_LIFETIME=5m
DB_CONN_MAX_IDLE_TIME=10m
```

---

## Appendix C: Troubleshooting Guide

### Problem: "DB_PASSWORD environment variable is required"

**Cause:** DB_PASSWORD not set

**Solution:**
```bash
export DB_PASSWORD=your_password
go run .
```

### Problem: "invalid DB_PORT: 99999"

**Cause:** Invalid port number

**Solution:**
```bash
export DB_PORT=5432  # Valid port
go run .
```

### Problem: "failed to ping database"

**Cause:** Database not accessible or credentials wrong

**Solution:**
```bash
# Verify database is running
psql -h localhost -U hddb -d hdd_db

# Check environment variables
env | grep DB_

# Test connection string
psql "host=localhost port=5432 user=hddb password=yourpass dbname=hdd_db"
```

### Problem: Application works but uses old hardcoded values

**Cause:** Old binary still running

**Solution:**
```bash
# Rebuild
go build -o hdd

# Restart service
systemctl restart bhandaar
```

---

**END OF DOCUMENT**
