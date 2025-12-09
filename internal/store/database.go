package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	_ "github.com/jackc/pgx/v4/stdlib"
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
	region            = "ap-south-1"

	appEnvVar     = "APP_ENV"
	localEnvVal   = "local"
	stagingEnvVal = "staging"
	prodEnvVal    = "prod"

	secretNameEnvVar = "DB_SECRET_NAME"
)

var (
	secretCache   = make(map[string]DBSecret)
	secretCacheMu sync.RWMutex
)

func Open() (*sql.DB, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	env, rawEnv, err := envLabel()
	if err != nil {
		return nil, err
	}

	dsn, secretUsed, err := resolveDSN(ctx, env)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("database ping: %w", err)
	}

	configureConnectionPool(db)

	slog.Info("Connected to Database, where",
		"env", env,
		"secret", secretUsed,
		"APP_ENV", rawEnv,
	)

	return db, nil
}

func resolveDSN(ctx context.Context, env string) (string, string, error) {
	if env == localEnvVal {
		return localDSNFromEnv(), "local-env-vars", nil
	}

	secretName := selectSecretName(env)
	secret, err := fetchDBSecret(ctx, secretName)
	if err != nil {
		return "", "", fmt.Errorf("failed to load database secret: %w", err)
	}

	return fmt.Sprintf(
		"postgresql://%s:%s@%s:%s/%s?sslmode=require",
		secret.Username,
		secret.Password,
		secret.Host,
		secret.Port,
		secret.Database,
	), secretName, nil
}

func localDSNFromEnv() string {
	host := getenvDefault("POSTGRES_HOST", "localhost")
	port := getenvDefault("POSTGRES_PORT", "5432")
	user := getenvDefault("POSTGRES_USER", "postgres")
	pass := getenvDefault("POSTGRES_PASSWORD", "postgres")
	db := getenvDefault("POSTGRES_DB", "postgres")

	return fmt.Sprintf(
		"postgresql://%s:%s@%s:%s/%s?sslmode=disable",
		user, pass, host, port, db,
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
		return prodEnvVal, raw, nil
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

func selectSecretName(env string) string {
	if override := strings.TrimSpace(os.Getenv(secretNameEnvVar)); override != "" {
		return override
	}

	switch env {
	case stagingEnvVal:
		return secretNameStaging
	case prodEnvVal:
		return secretNameProd
	default:
		return secretNameProd
	}
}

func fetchDBSecret(ctx context.Context, name string) (DBSecret, error) {
	if cached, ok := loadCachedSecret(name); ok {
		return cached, nil
	}

	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
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
	secretCacheMu.RLock()
	secret, ok := secretCache[name]
	secretCacheMu.RUnlock()
	return secret, ok
}

func storeCachedSecret(name string, secret DBSecret) {
	secretCacheMu.Lock()
	secretCache[name] = secret
	secretCacheMu.Unlock()
}

func configureConnectionPool(db *sql.DB) {
	maxOpen := getenvInt("DB_MAX_OPEN_CONNS", 10)
	maxIdle := getenvInt("DB_MAX_IDLE_CONNS", 5)
	connLifetime := getenvDuration("DB_CONN_MAX_LIFETIME", 30*time.Minute)

	db.SetMaxOpenConns(maxOpen)
	db.SetMaxIdleConns(maxIdle)
	db.SetConnMaxLifetime(connLifetime)
}
