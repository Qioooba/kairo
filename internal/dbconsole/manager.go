package dbconsole

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"kairo/internal/credentials"

	mysqldriver "github.com/go-sql-driver/mysql"
	redis "github.com/redis/go-redis/v9"
	go_ora "github.com/sijms/go-ora/v2"
)

type poolEntry struct {
	fingerprint string
	sql         *sql.DB
	redis       *redis.Client
}

type Manager struct {
	store         *Store
	mu            sync.Mutex
	pools         map[string]*poolEntry
	metadataCache map[string]metadataCacheEntry
	global        chan struct{}
}

func NewManager(dataDir string) (*Manager, error) {
	store, err := NewStore(dataDir)
	if err != nil {
		return nil, err
	}
	return &Manager{
		store:         store,
		pools:         make(map[string]*poolEntry),
		metadataCache: make(map[string]metadataCacheEntry),
		global:        make(chan struct{}, 8),
	}, nil
}

func (m *Manager) Store() *Store { return m.store }

func (m *Manager) Close() error {
	m.mu.Lock()
	entries := make([]*poolEntry, 0, len(m.pools))
	for id, entry := range m.pools {
		entries = append(entries, entry)
		delete(m.pools, id)
	}
	clear(m.metadataCache)
	m.mu.Unlock()
	var errs []error
	for _, entry := range entries {
		errs = append(errs, closePoolEntry(entry))
	}
	return errors.Join(errs...)
}

func (m *Manager) Invalidate(id string) {
	m.mu.Lock()
	entry := m.pools[id]
	delete(m.pools, id)
	m.invalidateMetadataLocked(id)
	m.mu.Unlock()
	if entry != nil {
		// sql.DB.Close can wait for in-flight queries. Source edits must not block
		// the admin request; active users already have a bounded QueryContext.
		go func() { _ = closePoolEntry(entry) }()
	}
}

func closePoolEntry(entry *poolEntry) error {
	if entry == nil {
		return nil
	}
	var errs []error
	if entry.sql != nil {
		errs = append(errs, entry.sql.Close())
	}
	if entry.redis != nil {
		errs = append(errs, entry.redis.Close())
	}
	return errors.Join(errs...)
}

func (m *Manager) acquire(ctx context.Context) error {
	select {
	case m.global <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) release() { <-m.global }

func sourceFingerprint(source Source) string {
	b, _ := json.Marshal(source)
	return string(b)
}

func (m *Manager) sqlDB(source Source) (*sql.DB, error) {
	fingerprint := sourceFingerprint(source)
	m.mu.Lock()
	if entry := m.pools[source.ID]; entry != nil && entry.sql != nil && entry.fingerprint == fingerprint {
		m.mu.Unlock()
		return entry.sql, nil
	}
	m.mu.Unlock()
	password, err := credentials.GetResource(CredentialNamespace, source.ID, source.CredentialUser())
	if err != nil {
		return nil, err
	}
	var driverName, dsn string
	switch source.Kind {
	case KindOracle:
		driverName = "oracle"
		options := map[string]string{
			"TIMEOUT":            strconv.Itoa(source.QueryTimeoutSeconds),
			"CONNECTION TIMEOUT": "10",
			"LOB FETCH":          "STREAM",
			// go-ora defaults to 25 rows. A moderate prefetch reduces Oracle 11g
			// network round trips without letting wide rows inflate memory sharply.
			"PREFETCH_ROWS": "50",
		}
		if source.OracleConnectBy == "sid" {
			options["SID"] = source.OracleService
		}
		if source.OracleClientCharset != "" {
			options["CLIENT CHARSET"] = source.OracleClientCharset
		}
		if source.TLSMode != "disabled" {
			options["SSL"] = "ENABLE"
			if source.TLSMode == "skip-verify" {
				options["SSL VERIFY"] = "FALSE"
			}
		}
		service := source.OracleService
		if source.OracleConnectBy == "sid" {
			service = ""
		}
		dsn = go_ora.BuildUrl(source.Host, source.Port, service, source.Username, password, options)
	case KindMySQL:
		driverName = "mysql"
		cfg := mysqldriver.NewConfig()
		cfg.User = source.Username
		cfg.Passwd = password
		cfg.Net = "tcp"
		cfg.Addr = net.JoinHostPort(source.Host, strconv.Itoa(source.Port))
		cfg.DBName = source.Database
		cfg.ParseTime = true
		cfg.Timeout = 10 * time.Second
		cfg.ReadTimeout = source.Timeout()
		cfg.WriteTimeout = source.Timeout()
		switch source.TLSMode {
		case "preferred":
			cfg.TLSConfig = "preferred"
		case "required":
			cfg.TLSConfig = "true"
		case "skip-verify":
			cfg.TLSConfig = "skip-verify"
		}
		dsn = cfg.FormatDSN()
	default:
		return nil, fmt.Errorf("%s 不是 SQL 数据源", source.Kind)
	}
	db, err := sql.Open(driverName, dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(source.MaxOpenConnections)
	db.SetMaxIdleConns(source.MaxIdleConnections)
	db.SetConnMaxLifetime(time.Duration(source.ConnectionMaxMinutes) * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)
	m.mu.Lock()
	if entry := m.pools[source.ID]; entry != nil && entry.sql != nil && entry.fingerprint == fingerprint {
		m.mu.Unlock()
		_ = db.Close()
		return entry.sql, nil
	}
	old := m.pools[source.ID]
	m.pools[source.ID] = &poolEntry{fingerprint: fingerprint, sql: db}
	m.mu.Unlock()
	if old != nil {
		go func() { _ = closePoolEntry(old) }()
	}
	return db, nil
}

func (m *Manager) redisClient(source Source) (*redis.Client, error) {
	fingerprint := sourceFingerprint(source)
	m.mu.Lock()
	if entry := m.pools[source.ID]; entry != nil && entry.redis != nil && entry.fingerprint == fingerprint {
		m.mu.Unlock()
		return entry.redis, nil
	}
	m.mu.Unlock()
	password, err := credentials.GetResource(CredentialNamespace, source.ID, source.CredentialUser())
	if err != nil && !errors.Is(err, credentials.ErrNotSaved) {
		return nil, err
	}
	if errors.Is(err, credentials.ErrNotSaved) {
		password = ""
	}
	opts := &redis.Options{
		Addr:         net.JoinHostPort(source.Host, strconv.Itoa(source.Port)),
		Username:     source.Username,
		Password:     password,
		DB:           source.RedisDB,
		DialTimeout:  10 * time.Second,
		ReadTimeout:  source.Timeout(),
		WriteTimeout: source.Timeout(),
		PoolSize:     source.MaxOpenConnections,
		MinIdleConns: source.MaxIdleConnections,
	}
	if source.TLSMode != "disabled" {
		opts.TLSConfig = &tls.Config{ServerName: source.Host, MinVersion: tls.VersionTLS12}
		if source.TLSMode == "skip-verify" {
			opts.TLSConfig.InsecureSkipVerify = true // explicit per-source administrative option
		}
	}
	client := redis.NewClient(opts)
	m.mu.Lock()
	if entry := m.pools[source.ID]; entry != nil && entry.redis != nil && entry.fingerprint == fingerprint {
		m.mu.Unlock()
		_ = client.Close()
		return entry.redis, nil
	}
	old := m.pools[source.ID]
	m.pools[source.ID] = &poolEntry{fingerprint: fingerprint, redis: client}
	m.mu.Unlock()
	if old != nil {
		go func() { _ = closePoolEntry(old) }()
	}
	return client, nil
}

type TestResult struct {
	OK        bool   `json:"ok"`
	Kind      string `json:"kind"`
	Version   string `json:"version,omitempty"`
	LatencyMS int64  `json:"latency_ms"`
}

func (m *Manager) withSQL(ctx context.Context, source Source, fn func(context.Context, *sql.DB) error) error {
	if source.Kind == KindRedis {
		return fmt.Errorf("Redis 不支持 SQL 元数据")
	}
	ctx, cancel := context.WithTimeout(ctx, source.Timeout())
	defer cancel()
	if err := m.acquire(ctx); err != nil {
		return err
	}
	defer m.release()
	db, err := m.sqlDB(source)
	if err != nil {
		return err
	}
	return fn(ctx, db)
}

func (m *Manager) Test(ctx context.Context, source Source) (TestResult, error) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := m.acquire(ctx); err != nil {
		return TestResult{}, err
	}
	defer m.release()
	result := TestResult{Kind: source.Kind}
	if source.Kind == KindRedis {
		client, err := m.redisClient(source)
		if err != nil {
			return result, err
		}
		info, err := client.Info(ctx, "server").Result()
		if err != nil {
			return result, err
		}
		result.Version = redisVersion(info)
	} else {
		db, err := m.sqlDB(source)
		if err != nil {
			return result, err
		}
		if err := db.PingContext(ctx); err != nil {
			return result, err
		}
		query := "SELECT VERSION()"
		if source.Kind == KindOracle {
			query = "SELECT banner FROM v$version WHERE ROWNUM = 1"
		}
		_ = db.QueryRowContext(ctx, query).Scan(&result.Version) // low-privilege users may not see v$version
	}
	result.OK = true
	result.LatencyMS = time.Since(started).Milliseconds()
	return result, nil
}

func redisVersion(info string) string {
	for _, line := range splitLines(info) {
		if len(line) > len("redis_version:") && line[:len("redis_version:")] == "redis_version:" {
			return line[len("redis_version:"):]
		}
	}
	return ""
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '\n' {
			line := s[start:i]
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			out = append(out, line)
			start = i + 1
		}
	}
	return out
}
