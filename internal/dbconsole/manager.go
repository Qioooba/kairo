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
	redis       redis.UniversalClient
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
			// Oracle 11g 优化：将驱动预取从 50 下调至 15 行。
			// 既减少网络往返，又彻底避免宽表或包含大对象（LOB）时驱动网络缓冲区一次性膨胀数 GB。
			"PREFETCH_ROWS": "15",
		}
		if source.OracleConnectBy == "sid" {
			options["SID"] = source.OracleService
		}
		if source.OracleClientCharset != "" {
			options["CLIENT CHARSET"] = source.OracleClientCharset
		} else {
			// 默认使用 AL32UTF8 避免 NLS_LANG 未配置时中文 CLOB/NVARCHAR2 出现乱码
			options["CLIENT CHARSET"] = "AL32UTF8"
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

func redisTLSConfig(source Source) *tls.Config {
	if source.TLSMode == "disabled" {
		return nil
	}
	cfg := &tls.Config{ServerName: source.Host, MinVersion: tls.VersionTLS12}
	if source.TLSMode == "skip-verify" {
		cfg.InsecureSkipVerify = true // explicit per-source administrative option
	}
	return cfg
}

func (m *Manager) redisClient(source Source) (redis.UniversalClient, error) {
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
	addrs := source.RedisAddrs()
	tlsConfig := redisTLSConfig(source)
	var client redis.UniversalClient
	switch source.RedisTopology() {
	case "cluster":
		client = redis.NewClusterClient(&redis.ClusterOptions{
			Addrs:        addrs,
			Username:     source.Username,
			Password:     password,
			DialTimeout:  10 * time.Second,
			ReadTimeout:  source.Timeout(),
			WriteTimeout: source.Timeout(),
			PoolSize:     source.MaxOpenConnections,
			MinIdleConns: source.MaxIdleConnections,
			TLSConfig:    tlsConfig,
		})
	case "sentinel":
		client = redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:    source.RedisMasterName,
			SentinelAddrs: addrs,
			Username:      source.Username,
			Password:      password,
			DB:            source.RedisDB,
			DialTimeout:   10 * time.Second,
			ReadTimeout:   source.Timeout(),
			WriteTimeout:  source.Timeout(),
			PoolSize:      source.MaxOpenConnections,
			MinIdleConns:  source.MaxIdleConnections,
			TLSConfig:     tlsConfig,
		})
	default:
		client = redis.NewClient(&redis.Options{
			Addr:         addrs[0],
			Username:     source.Username,
			Password:     password,
			DB:           source.RedisDB,
			DialTimeout:  10 * time.Second,
			ReadTimeout:  source.Timeout(),
			WriteTimeout: source.Timeout(),
			PoolSize:     source.MaxOpenConnections,
			MinIdleConns: source.MaxIdleConnections,
			TLSConfig:    tlsConfig,
		})
	}
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
	Topology  string `json:"topology,omitempty"`
	Masters   int    `json:"masters,omitempty"`
}

func (m *Manager) withSQL(ctx context.Context, source Source, fn func(context.Context, *sql.DB) error) error {
	if source.Kind == KindRedis {
		return fmt.Errorf("Redis 不支持 SQL 元数据")
	}
	timeout := source.Timeout()
	if timeout < 60*time.Second {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		last = m.withSQLAttempt(ctx, source, fn)
		if last == nil {
			return nil
		}
		if attempt == 0 && isConnectionFailure(last) {
			m.Invalidate(source.ID)
			continue
		}
		return last
	}
	return fmt.Errorf("连接重试失败: %w", last)
}

func (m *Manager) withSQLAttempt(ctx context.Context, source Source, fn func(context.Context, *sql.DB) error) error {
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
	var lastResult TestResult
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		lastResult, lastErr = m.testAttempt(ctx, source)
		if lastErr == nil {
			lastResult.OK = true
			lastResult.LatencyMS = time.Since(started).Milliseconds()
			return lastResult, nil
		}
		if attempt == 0 && isConnectionFailure(lastErr) {
			m.Invalidate(source.ID)
			continue
		}
		return lastResult, lastErr
	}
	return lastResult, fmt.Errorf("连接重试失败: %w", lastErr)
}

func (m *Manager) testAttempt(ctx context.Context, source Source) (TestResult, error) {
	if err := m.acquire(ctx); err != nil {
		return TestResult{}, err
	}
	defer m.release()
	result := TestResult{Kind: source.Kind}
	if source.Kind == KindRedis {
		result.Topology = source.RedisTopology()
		client, err := m.redisClient(source)
		if err != nil {
			return result, err
		}
		info, err := client.Info(ctx, "server").Result()
		if err != nil {
			return result, err
		}
		result.Version = redisVersion(info)
		if cluster, ok := client.(*redis.ClusterClient); ok {
			result.Topology = "cluster"
			if n, err := clusterMasterCount(ctx, cluster); err == nil {
				result.Masters = n
			}
		}
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
