package cli

import (
	"fmt"
	"mediahub_oss/internal/cli/config"
	"mediahub_oss/internal/repository"
	"mediahub_oss/internal/repository/postgres"
	"mediahub_oss/internal/repository/sqlite"
	"mediahub_oss/internal/storage"
	"mediahub_oss/internal/storage/localstorage"
	"mediahub_oss/internal/storage/s3storage"
)

// initRepository sets up the database connection based on the configuration.
func initRepository(dbCfg config.DatabaseConfig) (repository.Repository, error) {
	switch dbCfg.Driver {
	case "sqlite":
		return sqlite.NewRepository(dbCfg.Source)
	case "postgres":
		return postgres.NewRepository(dbCfg.Source, dbCfg.MaxOpenConns, dbCfg.MaxIdleConns)
	default:
		return nil, fmt.Errorf("unsupported database driver, must be `sqlite` or `postgres`: %s", dbCfg.Driver)
	}
}

// initStorage sets up the file storage provider based on the configuration.
func initStorage(storageCfg config.StorageConfig) (storage.StorageProvider, error) {
	switch storageCfg.Type {
	case "local":
		return &localstorage.LocalStorage{RootPath: storageCfg.Local.Root}, nil
	case "s3":
		s3prov, err := s3storage.NewS3StorageProvider(s3storage.Config{
			Endpoint:  storageCfg.S3.Endpoint,
			Region:    storageCfg.S3.Region,
			Bucket:    storageCfg.S3.Bucket,
			AccessKey: storageCfg.S3.AccessKey,
			SecretKey: storageCfg.S3.SecretKey,
			UseSSL:    storageCfg.S3.UseSSL,
		})
		if err != nil {
			return nil, err
		}
		return s3prov, nil
	default:
		return nil, fmt.Errorf("unsupported storage type, must be `local` or `s3`: %s", storageCfg.Type)
	}
}
