package dbconsole

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"kairo/internal/credentials"
	"kairo/internal/sshclient"

	mysqldriver "github.com/go-sql-driver/mysql"
	redis "github.com/redis/go-redis/v9"
)

type poolEntry struct {
	fingerprint string
	sql         *sql.DB
	redis       redis.UniversalClient
	ssh         *sshclient.Client
}

type transactionEntry struct {
	mu          sync.Mutex
	tx          *sql.Tx
	cancel      context.CancelFunc
	sourceID    string
	fingerprint string
	sessionID   string
	createdAt   time.Time
	updatedAt   time.Time
	done        bool
	outcome     TerminalOutcome
	outcomeErr  error
	outcomeMsg  string
}

type Manager struct {
	store               *Store
	mu                  sync.Mutex
	closed              atomic.Bool
	pools               map[string]*poolEntry
	metadataCache       map[string]metadataCacheEntry
	transactions        map[string]*transactionEntry
	terminalRecords     map[string]TransactionTerminalRecord
	global              chan struct{}
	transactionTTL      time.Duration
	transactionMax      int
	terminalTTL         time.Duration
	terminalMax         int
	transactionStop     chan struct{}
	transactionDone     chan struct{}
	transactionStopOnce sync.Once
	dpiFailedSources    sync.Map
}

func NewManager(dataDir string) (*Manager, error) {
	store, err := NewStore(dataDir)
	if err != nil {
		return nil, err
	}
	m := &Manager{
		store:           store,
		pools:           make(map[string]*poolEntry),
		metadataCache:   make(map[string]metadataCacheEntry),
		transactions:    make(map[string]*transactionEntry),
		terminalRecords: make(map[string]TransactionTerminalRecord),
		global:          make(chan struct{}, 8),
		transactionTTL:  defaultTransactionIdleTTL,
		transactionMax:  defaultTransactionMax,
		terminalTTL:     defaultTransactionIdleTTL,
		terminalMax:     defaultTransactionMax * 4,
		transactionStop: make(chan struct{}),
		transactionDone: make(chan struct{}),
	}
	go m.transactionJanitor()
	return m, nil
}

func (m *Manager) Store() *Store { return m.store }

func (m *Manager) Close() error {
	if m.closed.Swap(true) {
		return nil
	}
	m.stopTransactionJanitor()
	m.mu.Lock()
	entries := make([]*poolEntry, 0, len(m.pools))
	for id, entry := range m.pools {
		entries = append(entries, entry)
		delete(m.pools, id)
	}
	clear(m.metadataCache)
	txs := make([]*transactionEntry, 0, len(m.transactions))
	for id, entry := range m.transactions {
		txs = append(txs, entry)
		delete(m.transactions, id)
	}
	clear(m.terminalRecords)
	m.mu.Unlock()
	var errs []error
	for _, entry := range txs {
		entry.mu.Lock()
		if entry.tx != nil {
			errs = append(errs, entry.tx.Rollback())
		}
		if entry.cancel != nil {
			entry.cancel()
		}
		entry.mu.Unlock()
	}
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
	var txs []*transactionEntry
	for key, tx := range m.transactions {
		if tx.sourceID == id {
			txs = append(txs, tx)
			delete(m.transactions, key)
		}
	}
	for key, r := range m.terminalRecords {
		if r.SourceID == id {
			delete(m.terminalRecords, key)
		}
	}
	m.mu.Unlock()
	for _, tx := range txs {
		tx.mu.Lock()
		if tx.tx != nil {
			_ = tx.tx.Rollback()
		}
		if tx.cancel != nil {
			tx.cancel()
		}
		tx.mu.Unlock()
	}
	if entry != nil {
		// sql.DB.Close can wait for in-flight queries. Source edits must not block
		// the admin request; active users already have a bounded QueryContext.
		go func() { _ = closePoolEntry(entry) }()
	}
}

// A retry must not tear down unrelated tabs or their shared SSH transport.
// With live transactions, let database/sql discard broken idle connections.
func (m *Manager) invalidatePool(id string) {
	m.mu.Lock()
	for _, tx := range m.transactions {
		if tx != nil && tx.sourceID == id {
			m.mu.Unlock()
			return
		}
	}
	entry := m.pools[id]
	delete(m.pools, id)
	m.invalidateMetadataLocked(id)
	m.mu.Unlock()
	if entry != nil {
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
	if entry.ssh != nil {
		errs = append(errs, entry.ssh.Close())
	}
	return errors.Join(errs...)
}

func (m *Manager) acquire(ctx context.Context) error {
	if m.closed.Load() {
		return errors.New("dbconsole manager is closed")
	}
	select {
	case m.global <- struct{}{}:
		if m.closed.Load() {
			m.release()
			return errors.New("dbconsole manager is closed")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) release() { <-m.global }

// SourceFingerprint generates a stable configuration fingerprint for a Source.
func SourceFingerprint(source Source) string {
	return sourceFingerprint(source)
}

func sourceFingerprint(source Source) string {
	b, _ := json.Marshal(source)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (m *Manager) sqlDB(source Source) (*sql.DB, error) {
	if m.closed.Load() {
		return nil, errors.New("dbconsole manager is closed")
	}
	fingerprint := sourceFingerprint(source)
	m.mu.Lock()
	if m.closed.Load() {
		m.mu.Unlock()
		return nil, errors.New("dbconsole manager is closed")
	}
	if entry := m.pools[source.ID]; entry != nil && entry.sql != nil && entry.fingerprint == fingerprint {
		m.mu.Unlock()
		return entry.sql, nil
	}
	m.mu.Unlock()
	password, err := credentials.GetResource(CredentialNamespace, source.ID, source.CredentialUser())
	if err != nil {
		return nil, err
	}
	tunnel, err := openSSHTunnel(context.Background(), source)
	if err != nil {
		return nil, err
	}
	closeTunnelOnError := true
	defer func() {
		if closeTunnelOnError && tunnel != nil {
			_ = tunnel.Close()
		}
	}()
	tlsConfig, err := tlsConfigForSource(source)
	if err != nil {
		return nil, err
	}
	targetHost, targetPort := sourceConnectionTarget(source)
	db, err := m.buildSQLDB(source, password, tunnel, tlsConfig, targetHost, targetPort)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(source.MaxOpenConnections)
	db.SetMaxIdleConns(source.MaxIdleConnections)
	db.SetConnMaxLifetime(time.Duration(source.ConnectionMaxMinutes) * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)
	m.mu.Lock()
	if m.closed.Load() {
		m.mu.Unlock()
		_ = db.Close()
		if tunnel != nil {
			_ = tunnel.Close()
		}
		return nil, errors.New("dbconsole manager is closed")
	}
	if entry := m.pools[source.ID]; entry != nil && entry.sql != nil && entry.fingerprint == fingerprint {
		m.mu.Unlock()
		_ = db.Close()
		if tunnel != nil {
			_ = tunnel.Close()
		}
		return entry.sql, nil
	}
	old := m.pools[source.ID]
	m.pools[source.ID] = &poolEntry{fingerprint: fingerprint, sql: db, ssh: tunnel}
	m.mu.Unlock()
	closeTunnelOnError = false
	if old != nil {
		go func() { _ = closePoolEntry(old) }()
	}
	return db, nil
}

func (m *Manager) buildSQLDB(source Source, password string, tunnel *sshclient.Client, tlsConfig *tls.Config, targetHost string, targetPort int) (*sql.DB, error) {
	var dialer funcDialer
	if tunnel != nil {
		dialer = funcDialer{dial: sshTunnelDialer{client: tunnel.RawConn()}}
	}
	var driverName, dsn string
	var connector driver.Connector
	var oracleBackend OracleBackend
	switch source.Kind {
	case KindOracle:
		driverName = "oracle"
		hasDialer := tunnel != nil
		oracleBackend = m.ResolveOracleBackend(source)
		c, dsnStr, err := oracleBackend.OpenConnector(source, password, dialer, hasDialer, tlsConfig, targetHost, targetPort)
		if err != nil {
			if strings.ToLower(strings.TrimSpace(source.OracleDriver)) != "godror" && oracleBackend.Name() == "godror" {
				if source.ID != "" {
					m.dpiFailedSources.Store(source.ID, true)
				}
				oracleBackend = &GoOraBackend{}
				c, dsnStr, err = oracleBackend.OpenConnector(source, password, dialer, hasDialer, tlsConfig, targetHost, targetPort)
			}
			if err != nil {
				return nil, err
			}
		}
		connector = c
		dsn = dsnStr
	case KindMySQL:
		driverName = "mysql"
		cfg := mysqldriver.NewConfig()
		cfg.User = source.Username
		cfg.Passwd = password
		cfg.Net = "tcp"
		cfg.Addr = net.JoinHostPort(targetHost, strconv.Itoa(targetPort))
		cfg.DBName = source.Database
		cfg.ParseTime = true
		cfg.Timeout = 10 * time.Second
		cfg.ReadTimeout = source.Timeout()
		cfg.WriteTimeout = source.Timeout()
		if tlsConfig != nil {
			cfg.TLS = tlsConfig
		}
		if tunnel != nil {
			cfg.DialFunc = dialer.DialContext
		}
		dsn = cfg.FormatDSN()
	default:
		return nil, fmt.Errorf("%s 不是 SQL 数据源", source.Kind)
	}
	var db *sql.DB
	var err error
	if connector != nil {
		db = sql.OpenDB(connector)
	} else {
		db, err = sql.Open(driverName, dsn)
	}
	if err != nil {
		return nil, err
	}
	if source.Kind == KindOracle && strings.ToLower(strings.TrimSpace(source.OracleDriver)) != "godror" && oracleBackend != nil && oracleBackend.Name() == "godror" && connector != nil {
		testCtx, testCancel := context.WithTimeout(context.Background(), 2*time.Second)
		pingErr := db.PingContext(testCtx)
		testCancel()
		if pingErr != nil && IsOracleDPIError(pingErr) {
			if source.ID != "" {
				m.dpiFailedSources.Store(source.ID, true)
			}
			_ = db.Close()
			gooraBackend := &GoOraBackend{}
			hasDialer := tunnel != nil
			if c, s, fbErr := gooraBackend.OpenConnector(source, password, dialer, hasDialer, tlsConfig, targetHost, targetPort); fbErr == nil {
				if c != nil {
					db = sql.OpenDB(c)
				} else {
					db, _ = sql.Open("oracle", s)
				}
			}
		}
	}
	return db, nil
}

func redisTLSConfig(source Source) *tls.Config {
	cfg, err := tlsConfigForSource(source)
	if err != nil {
		return nil
	}
	return cfg
}

func (m *Manager) redisClient(source Source) (redis.UniversalClient, error) {
	if m.closed.Load() {
		return nil, errors.New("dbconsole manager is closed")
	}
	fingerprint := sourceFingerprint(source)
	m.mu.Lock()
	if m.closed.Load() {
		m.mu.Unlock()
		return nil, errors.New("dbconsole manager is closed")
	}
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
	tlsConfig, err := tlsConfigForSource(source)
	if err != nil {
		return nil, err
	}
	tunnel, err := openSSHTunnel(context.Background(), source)
	if err != nil {
		return nil, err
	}
	addrs := redisConnectionAddrs(source)
	closeTunnelOnError := true
	defer func() {
		if closeTunnelOnError && tunnel != nil {
			_ = tunnel.Close()
		}
	}()
	var dialer funcDialer
	var dialFunc func(context.Context, string, string) (net.Conn, error)
	if tunnel != nil {
		dialer = funcDialer{dial: sshTunnelDialer{client: tunnel.RawConn()}}
		dialFunc = dialer.DialContext
	}
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
			Dialer:       dialFunc,
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
			Dialer:        dialFunc,
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
			Dialer:       dialFunc,
		})
	}
	m.mu.Lock()
	if m.closed.Load() {
		m.mu.Unlock()
		_ = client.Close()
		if tunnel != nil {
			_ = tunnel.Close()
		}
		return nil, errors.New("dbconsole manager is closed")
	}
	if entry := m.pools[source.ID]; entry != nil && entry.redis != nil && entry.fingerprint == fingerprint {
		m.mu.Unlock()
		_ = client.Close()
		if tunnel != nil {
			_ = tunnel.Close()
		}
		return entry.redis, nil
	}
	old := m.pools[source.ID]
	m.pools[source.ID] = &poolEntry{fingerprint: fingerprint, redis: client, ssh: tunnel}
	m.mu.Unlock()
	closeTunnelOnError = false
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
			m.invalidatePool(source.ID)
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
	return m.TestWithCredentials(ctx, source, "", "")
}

func (m *Manager) TestWithCredentials(ctx context.Context, source Source, customPassword, customSSHPassword string) (TestResult, error) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var lastResult TestResult
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		lastResult, lastErr = m.testAttemptWithCredentials(ctx, source, customPassword, customSSHPassword)
		if lastErr == nil {
			lastResult.OK = true
			lastResult.LatencyMS = time.Since(started).Milliseconds()
			return lastResult, nil
		}
		if attempt == 0 && isConnectionFailure(lastErr) {
			if source.ID != "" && customPassword == "" && customSSHPassword == "" {
				m.invalidatePool(source.ID)
			}
			continue
		}
		return lastResult, lastErr
	}
	return lastResult, fmt.Errorf("连接重试失败: %w", lastErr)
}

func (m *Manager) testAttempt(ctx context.Context, source Source) (TestResult, error) {
	return m.testAttemptWithCredentials(ctx, source, "", "")
}

func (m *Manager) testAttemptWithCredentials(ctx context.Context, source Source, customPassword, customSSHPassword string) (TestResult, error) {
	if customPassword == "" && customSSHPassword == "" && source.ID != "" {
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
			_ = db.QueryRowContext(ctx, query).Scan(&result.Version)
		}
		return result, nil
	}

	// 针对自定义密码或新建草稿的独立连通性检测（不污染连接池）
	if err := m.acquire(ctx); err != nil {
		return TestResult{}, err
	}
	defer m.release()
	result := TestResult{Kind: source.Kind}

	if source.Kind == KindRedis {
		result.Topology = source.RedisTopology()
		password := customPassword
		if password == "" && source.ID != "" {
			var err error
			password, err = credentials.GetResource(CredentialNamespace, source.ID, source.CredentialUser())
			if err != nil && !errors.Is(err, credentials.ErrNotSaved) {
				return result, err
			}
		}
		tlsConfig, err := tlsConfigForSource(source)
		if err != nil {
			return result, err
		}
		tunnel, err := openSSHTunnelWithPassword(ctx, source, customSSHPassword)
		if err != nil {
			return result, err
		}
		if tunnel != nil {
			defer tunnel.Close()
		}
		addrs := redisConnectionAddrs(source)
		var dialFunc func(context.Context, string, string) (net.Conn, error)
		if tunnel != nil {
			dialer := funcDialer{dial: sshTunnelDialer{client: tunnel.RawConn()}}
			dialFunc = dialer.DialContext
		}
		var client redis.UniversalClient
		switch source.RedisTopology() {
		case "cluster":
			client = redis.NewClusterClient(&redis.ClusterOptions{
				Addrs: addrs, Username: source.Username, Password: password,
				DialTimeout: 10 * time.Second, ReadTimeout: source.Timeout(), WriteTimeout: source.Timeout(),
				PoolSize: 1, TLSConfig: tlsConfig, Dialer: dialFunc,
			})
		case "sentinel":
			client = redis.NewFailoverClient(&redis.FailoverOptions{
				MasterName: source.RedisMasterName, SentinelAddrs: addrs,
				Username: source.Username, Password: password,
				DialTimeout: 10 * time.Second, ReadTimeout: source.Timeout(), WriteTimeout: source.Timeout(),
				PoolSize: 1, TLSConfig: tlsConfig, Dialer: dialFunc,
			})
		default:
			addr := ""
			if len(addrs) > 0 {
				addr = addrs[0]
			}
			client = redis.NewClient(&redis.Options{
				Addr: addr, Username: source.Username, Password: password, DB: source.RedisDB,
				DialTimeout: 10 * time.Second, ReadTimeout: source.Timeout(), WriteTimeout: source.Timeout(),
				PoolSize: 1, TLSConfig: tlsConfig, Dialer: dialFunc,
			})
		}
		defer client.Close()
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
		return result, nil
	}

	// SQL (Oracle, MySQL) 独立测试
	password := customPassword
	if password == "" && source.ID != "" {
		var err error
		password, err = credentials.GetResource(CredentialNamespace, source.ID, source.CredentialUser())
		if err != nil {
			return result, err
		}
	}
	if password == "" && source.Kind != KindRedis {
		return result, errors.New("缺少数据库密码，请先输入密码")
	}
	tunnel, err := openSSHTunnelWithPassword(ctx, source, customSSHPassword)
	if err != nil {
		return result, err
	}
	if tunnel != nil {
		defer tunnel.Close()
	}
	tlsConfig, err := tlsConfigForSource(source)
	if err != nil {
		return result, err
	}
	targetHost, targetPort := sourceConnectionTarget(source)
	db, err := m.buildSQLDB(source, password, tunnel, tlsConfig, targetHost, targetPort)
	if err != nil {
		return result, err
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return result, err
	}
	query := "SELECT VERSION()"
	if source.Kind == KindOracle {
		query = "SELECT banner FROM v$version WHERE ROWNUM = 1"
	}
	_ = db.QueryRowContext(ctx, query).Scan(&result.Version)
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
