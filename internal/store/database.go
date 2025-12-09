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

type DBSecret struct {
	Host     string `json:"host"`
	Port     string `json:"port"`
	Database string `json:"dbname"`
	Username string `json:"username"`
	Password string `json:"password"`
}

const (
	secretNameProd    = "prod/gym/postgresql"
	secretNameStaging = "stagging/gym/postgresql"

	appEnvVar     = "APP_ENV"
	localEnvVal   = "local"
	stagingEnvVal = "staging"
	prodEnvVal    = "prod"

	secretNameEnvVar = "DB_SECRET_NAME"
)

var (
	secretCache   = make(map[string]cachedSecret)
	secretCacheMu sync.RWMutex

	awsCfg     aws.Config
	awsCfgOnce sync.Once
	awsCfgErr  error
)

type cachedSecret struct {
	secret    DBSecret
	expiresAt time.Time
}

// DB wraps *sql.DB to add helpers.
type DB struct {
	*sql.DB
}

type SecretProvider interface {
	GetSecret(ctx context.Context, name string) (DBSecret, error)
}

type defaultSecretProvider struct{}

func (defaultSecretProvider) GetSecret(ctx context.Context, name string) (DBSecret, error) {
	return fetchDBSecret(ctx, name)
}

func Open(ctx context.Context) (*DB, error) {
	return OpenWithProvider(ctx, defaultSecretProvider{})
}

func OpenWithProvider(ctx context.Context, provider SecretProvider) (*DB, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	timeout := getenvDuration("DB_CONNECT_TIMEOUT", 10*time.Second)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	env, rawEnv, err := envLabel()
	if err != nil {
		return nil, err
	}

	dsn, secretUsed, err := resolveDSN(ctx, env, provider)
	if err != nil {
		return nil, err
	}

	db, err := openWithRetry(ctx, dsn)
	if err != nil {
		return nil, err
	}

	configureConnectionPool(db)

	slog.Info("Connected to Database, where",
		"env", env,
		"secret", secretUsed,
		"APP_ENV", rawEnv,
	)

	return &DB{DB: db}, nil
}

func resolveDSN(ctx context.Context, env string, provider SecretProvider) (string, string, error) {
	if env == localEnvVal {
		return localDSNFromEnv(), "local-env-vars", nil
	}

	sslMode := getenvDefault("DB_SSL_MODE", "require")

	secretName, err := selectSecretName(env)
	if err != nil {
		return "", "", err
	}

	secret, err := provider.GetSecret(ctx, secretName)
	if err != nil {
		return "", "", fmt.Errorf("failed to load database secret: %w", err)
	}

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

	for i := 0; i < 3; i++ {
		db, err = sql.Open("pgx", dsn)
		if err == nil {
			if err = db.PingContext(ctx); err == nil {
				return db, nil
			}
			db.Close()
		}

		if i < 2 {
			time.Sleep(time.Second * time.Duration(1<<i))
		}
	}

	return nil, fmt.Errorf("failed to open database after retries: %w", err)
}

func localDSNFromEnv() string {
	host := getenvDefault("POSTGRES_HOST", "localhost")
	port := getenvDefault("POSTGRES_PORT", "5432")
	user := getenvDefault("POSTGRES_USER", "postgres")
	pass := getenvDefault("POSTGRES_PASSWORD", "postgres")
	db := getenvDefault("POSTGRES_DB", "postgres")
	sslMode := getenvDefault("DB_SSL_MODE", "disable")

	return fmt.Sprintf(
		"postgresql://%s:%s@%s:%s/%s?sslmode=%s",
		user, pass, host, port, db, sslMode,
	)
}

func getenvDefault(key, def string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return def
}

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
	raw := strings.TrimSpace(os.Getenv(appEnvVar))
	env := strings.ToLower(raw)

	switch {
	case env == "":
		return localEnvVal, raw, nil
	case strings.HasPrefix(env, "loc"):
		return localEnvVal, raw, nil
	case strings.HasPrefix(env, "stag"):
		return stagingEnvVal, raw, nil
	case strings.HasPrefix(env, "prod"):
		return prodEnvVal, raw, nil
	default:
		return "", raw, fmt.Errorf("unknown APP_ENV value %q (expected local|staging|prod)", raw)
	}
}

func selectSecretName(env string) (string, error) {
	if override := strings.TrimSpace(os.Getenv(secretNameEnvVar)); override != "" {
		return override, nil
	}

	switch env {
	case stagingEnvVal:
		return secretNameStaging, nil
	case prodEnvVal:
		return secretNameProd, nil
	default:
		return "", fmt.Errorf("no secret name configured for env: %s", env)
	}
}

func getAWSRegion() string {
	if r := strings.TrimSpace(os.Getenv("AWS_REGION")); r != "" {
		return r
	}
	return "ap-south-1"
}

func awsConfig(ctx context.Context) (aws.Config, error) {
	awsCfgOnce.Do(func() {
		awsCfg, awsCfgErr = config.LoadDefaultConfig(ctx, config.WithRegion(getAWSRegion()))
	})
	return awsCfg, awsCfgErr
}

func fetchDBSecret(ctx context.Context, name string) (DBSecret, error) {
	if cached, ok := loadCachedSecret(name); ok {
		return cached, nil
	}

	cfg, err := awsConfig(ctx)
	if err != nil {
		return DBSecret{}, fmt.Errorf("load aws config: %w", err)
	}

	svc := secretsmanager.NewFromConfig(cfg)
	input := &secretsmanager.GetSecretValueInput{
		SecretId:     aws.String(name),
		VersionStage: aws.String("AWSCURRENT"),
	}

	result, err := svc.GetSecretValue(ctx, input)
	if err != nil {
		return DBSecret{}, fmt.Errorf("get secret value: %w", err)
	}

	if result.SecretString == nil {
		return DBSecret{}, fmt.Errorf("secret value missing string payload")
	}

	var secret DBSecret
	if err := json.Unmarshal([]byte(*result.SecretString), &secret); err != nil {
		return DBSecret{}, fmt.Errorf("decode secret json: %w", err)
	}

	storeCachedSecret(name, secret)

	return secret, nil
}

func loadCachedSecret(name string) (DBSecret, bool) {
	secretCacheMu.Lock()
	entry, ok := secretCache[name]
	defer secretCacheMu.Unlock()
	if !ok {
		return DBSecret{}, false
	}

	if time.Now().After(entry.expiresAt) {
		delete(secretCache, name)
		return DBSecret{}, false
	}

	return entry.secret, true
}

func storeCachedSecret(name string, secret DBSecret) {
	secretCacheMu.Lock()
	secretCache[name] = cachedSecret{
		secret:    secret,
		expiresAt: time.Now().Add(getSecretCacheTTL()),
	}
	secretCacheMu.Unlock()
}

func getSecretCacheTTL() time.Duration {
	return getenvDuration("DB_SECRET_CACHE_TTL", 5*time.Minute)
}

func configureConnectionPool(db *sql.DB) {
	maxOpen := getenvInt("DB_MAX_OPEN_CONNS", 10)
	maxIdle := getenvInt("DB_MAX_IDLE_CONNS", 5)
	if maxIdle > maxOpen {
		maxIdle = maxOpen
	}
	connLifetime := getenvDuration("DB_CONN_MAX_LIFETIME", 30*time.Minute)

	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(connLifetime)
}

// HealthCheck pings the database with a short timeout.
func (db *DB) HealthCheck(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	return db.PingContext(ctx)
}
