package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// [0.1] shape of database secret payload
type DBSecret struct {
	Host     string `json:"host"`
	Port     string `json:"port"`
	Database string `json:"dbname"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// [0.2] default secret names and env keys
const (
	secretNameProd    = "prod/gym/postgresql"
	secretNameStaging = "stagging/gym/postgresql"

	appEnvVar     = "APP_ENV"
	localEnvVal   = "local"
	stagingEnvVal = "staging"
	prodEnvVal    = "prod"

	secretNameEnvVar = "DB_SECRET_NAME"
)

// [0.3] global caches and AWS config singletons
var (
	secretCache   = make(map[string]cachedSecret)
	secretCacheMu sync.RWMutex

	awsCfg     aws.Config
	awsCfgOnce sync.Once
	awsCfgErr  error
)

// [0.4] cached secret entry with expiry
type cachedSecret struct {
	secret    DBSecret
	expiresAt time.Time
}

// DB wraps *sql.DB to add helpers.
// [0.5] wrapper DB type for helper methods
type DB struct {
	*sql.DB
}

// [0.6] abstraction for secret retrieval
type SecretProvider interface {
	GetSecret(ctx context.Context, name string) (DBSecret, error)
}

// [0.7] default provider implementation
type defaultSecretProvider struct{}

func (defaultSecretProvider) GetSecret(ctx context.Context, name string) (DBSecret, error) {
	return fetchDBSecret(ctx, name)
}

func Open(ctx context.Context) (*DB, error) {
	// [1] delegate to OpenWithProvider
	return OpenWithProvider(ctx, defaultSecretProvider{})
}

func OpenWithProvider(ctx context.Context, provider SecretProvider) (*DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	// [2] read connect timeout configuration
	timeout := getenvDuration("DB_CONNECT_TIMEOUT", 10*time.Second)
	// [3] apply timeout to context
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// [4] normalize environment label
	env, rawEnv, err := envLabel()
	if err != nil {
		return nil, err
	}

	// [5] resolve DSN based on environment
	dsn, secretUsed, err := resolveDSN(ctx, env, provider)
	if err != nil {
		return nil, err
	}

	// [6] open database connection with retry
	db, err := openWithRetry(ctx, dsn)
	if err != nil {
		return nil, err
	}

	// [7] configure connection pooling
	configureConnectionPool(db)

	// [8] log successful connection
	slog.Info("Connected to Database, where",
		"env", env,
		"secret", secretUsed,
		"APP_ENV", rawEnv,
	)

	return &DB{DB: db}, nil
}

func resolveDSN(ctx context.Context, env string, provider SecretProvider) (string, string, error) {
	if env == localEnvVal {
		// [5.1] (local path) build DSN from local env vars
		return localDSNFromEnv(), "local-env-vars", nil
	}

	// [5.2] (non-local path) build DSN from secret-managed credentials
	// [5.2.1] read sslmode override
	sslMode := getenvDefault("DB_SSL_MODE", "require")

	// [5.2.2] pick secret name for environment
	secretName, err := selectSecretName(env)
	if err != nil {
		return "", "", err
	}

	// [5.2.3] fetch database secret
	secret, err := provider.GetSecret(ctx, secretName)
	if err != nil {
		return "", "", fmt.Errorf("failed to load database secret: %w", err)
	}

	// [5.2.4] format DSN from secret values
	return fmt.Sprintf(
		"postgresql://%s:%s@%s:%s/%s?sslmode=%s",
		url.QueryEscape(secret.Username),
		url.QueryEscape(secret.Password),
		secret.Host,
		secret.Port,
		secret.Database,
		sslMode,
	), secretName, nil
}

func openWithRetry(ctx context.Context, dsn string) (*sql.DB, error) {
	var db *sql.DB
	var err error

	// [6.1] attempt (i+1) to open and ping with exponential backoff
	for i := 0; i < 3; i++ {
		db, err = sql.Open("pgx", dsn)
		if err == nil {
			if err = db.PingContext(ctx); err == nil {
				return db, nil
			}
			db.Close()
		}

		if i < 2 {
			// [6.2] sleep before next retry
			time.Sleep(time.Second * time.Duration(1<<i))
		}
	}

	return nil, fmt.Errorf("failed to open database after retries: %w", err)
}

func localDSNFromEnv() string {
	// [5.1.1] read local connection env vars
	host := getenvDefault("POSTGRES_HOST", "localhost")
	port := getenvDefault("POSTGRES_PORT", "5432")
	user := getenvDefault("POSTGRES_USER", "postgres")
	pass := getenvDefault("POSTGRES_PASSWORD", "postgres")
	db := getenvDefault("POSTGRES_DB", "postgres")
	sslMode := getenvDefault("DB_SSL_MODE", "disable")

	// [5.1.2] format local DSN
	return fmt.Sprintf(
		"postgresql://%s:%s@%s:%s/%s?sslmode=%s",
		user, pass, host, port, db, sslMode,
	)
}

// [0.8] helper: env with default (string)
func getenvDefault(key, def string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return def
}

// [0.9] helper: env with default (int)
func getenvInt(key string, def int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}

	val, err := strconv.Atoi(raw)
	if err != nil {
		slog.Warn("invalid int env, using default", "key", key, "value", raw, "default", def, "err", err)
		return def
	}

	return val
}

// [0.10] helper: env with default (duration)
func getenvDuration(key string, def time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}

	val, err := time.ParseDuration(raw)
	if err != nil {
		slog.Warn("invalid duration env, using default", "key", key, "value", raw, "default", def, "err", err)
		return def
	}

	return val
}

func envLabel() (string, string, error) {
	// [4.1] read APP_ENV raw value
	raw := strings.TrimSpace(os.Getenv(appEnvVar))
	// [4.2] normalize to lowercase for comparison
	env := strings.ToLower(raw)

	switch {
	case env == "":
		// [4.3] default to local when unset
		return localEnvVal, raw, nil
	case strings.HasPrefix(env, "loc"):
		// [4.4] treat variants as local
		return localEnvVal, raw, nil
	case strings.HasPrefix(env, "stag"):
		// [4.5] staging path
		return stagingEnvVal, raw, nil
	case strings.HasPrefix(env, "prod"):
		// [4.6] production path
		return prodEnvVal, raw, nil
	default:
		// [4.7] unknown environment value error
		return "", raw, fmt.Errorf("unknown APP_ENV value %q (expected local|staging|prod)", raw)
	}
}

func selectSecretName(env string) (string, error) {
	// [5.2.2.1] allow override via env var
	if override := strings.TrimSpace(os.Getenv(secretNameEnvVar)); override != "" {
		return override, nil
	}

	switch env {
	case stagingEnvVal:
		// [5.2.2.2] staging secret selection
		return secretNameStaging, nil
	case prodEnvVal:
		// [5.2.2.3] production secret selection
		return secretNameProd, nil
	default:
		// [5.2.2.4] error when no secret configured
		return "", fmt.Errorf("no secret name configured for env: %s", env)
	}
}

func getAWSRegion() string {
	// [5.2.3.2.1] prefer AWS_REGION override
	if r := strings.TrimSpace(os.Getenv("AWS_REGION")); r != "" {
		return r
	}
	// [5.2.3.2.2] fall back to default region
	return "ap-south-1"
}

func awsConfig(ctx context.Context) (aws.Config, error) {
	// [5.2.3.2.3] load AWS config once per process
	awsCfgOnce.Do(func() {
		awsCfg, awsCfgErr = config.LoadDefaultConfig(ctx, config.WithRegion(getAWSRegion()))
	})
	return awsCfg, awsCfgErr
}

func fetchDBSecret(ctx context.Context, name string) (DBSecret, error) {
	// [5.2.3.1] check cached secret
	if cached, ok := loadCachedSecret(name); ok {
		return cached, nil
	}

	// [5.2.3.2] load AWS configuration
	cfg, err := awsConfig(ctx)
	if err != nil {
		return DBSecret{}, fmt.Errorf("load aws config: %w", err)
	}

	// [5.2.3.3] initialize secrets manager client
	svc := secretsmanager.NewFromConfig(cfg)
	input := &secretsmanager.GetSecretValueInput{
		SecretId:     aws.String(name),
		VersionStage: aws.String("AWSCURRENT"),
	}

	// [5.2.3.4] fetch secret value from AWS
	result, err := svc.GetSecretValue(ctx, input)
	if err != nil {
		return DBSecret{}, fmt.Errorf("get secret value: %w", err)
	}

	if result.SecretString == nil {
		return DBSecret{}, fmt.Errorf("secret value missing string payload")
	}

	// [5.2.3.5] decode secret JSON payload
	var secret DBSecret
	if err := json.Unmarshal([]byte(*result.SecretString), &secret); err != nil {
		return DBSecret{}, fmt.Errorf("decode secret json: %w", err)
	}

	// [5.2.3.6] cache decoded secret for reuse
	storeCachedSecret(name, secret)

	return secret, nil
}

func loadCachedSecret(name string) (DBSecret, bool) {
	// [5.2.3.1.1] acquire cache lock
	secretCacheMu.Lock()
	entry, ok := secretCache[name]
	defer secretCacheMu.Unlock()
	if !ok {
		// [5.2.3.1.2] no cache entry found
		return DBSecret{}, false
	}

	if time.Now().After(entry.expiresAt) {
		// [5.2.3.1.3] evict expired entry
		delete(secretCache, name)
		return DBSecret{}, false
	}

	// [5.2.3.1.4] return valid cached secret
	return entry.secret, true
}

func storeCachedSecret(name string, secret DBSecret) {
	// [5.2.3.6.1] store secret with TTL
	secretCacheMu.Lock()
	secretCache[name] = cachedSecret{
		secret:    secret,
		expiresAt: time.Now().Add(getSecretCacheTTL()),
	}
	secretCacheMu.Unlock()
}

func getSecretCacheTTL() time.Duration {
	// [5.2.3.6.2] read cache TTL override
	return getenvDuration("DB_SECRET_CACHE_TTL", 5*time.Minute)
}

func configureConnectionPool(db *sql.DB) {
	// [7.1] read pool size configuration
	maxOpen := getenvInt("DB_MAX_OPEN_CONNS", 10)
	maxIdle := getenvInt("DB_MAX_IDLE_CONNS", 5)
	if maxIdle > maxOpen {
		// [7.2] cap idle connections to max open
		maxIdle = maxOpen
	}
	// [7.3] read connection lifetime
	connLifetime := getenvDuration("DB_CONN_MAX_LIFETIME", 30*time.Minute)

	// [7.4] apply pool settings
	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(connLifetime)
}

// HealthCheck pings the database with a short timeout.
// [9] optional health check call path
func (db *DB) HealthCheck(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	return db.PingContext(ctx)
}
