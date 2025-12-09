package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	secretName = "prod/gym/postgresql"
	region     = "ap-south-1"
)

func Open() (*sql.DB, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	secret, err := fetchDBSecret(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load database secret: %w", err)
	}

	dsn := fmt.Sprintf(
		"postgresql://%s:%s@%s:%s/%s?sslmode=require",
		secret.Username,
		secret.Password,
		secret.Host,
		secret.Port,
		secret.Database,
	)

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database connection: %w", err)
	}

	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("database ping: %w", err)
	}

	fmt.Println("connected to the database")

	return db, nil
}

func fetchDBSecret(ctx context.Context) (DBSecret, error) {
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return DBSecret{}, fmt.Errorf("load aws config: %w", err)
	}

	svc := secretsmanager.NewFromConfig(cfg)
	input := &secretsmanager.GetSecretValueInput{
		SecretId:     aws.String(secretName),
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

	return secret, nil
}
