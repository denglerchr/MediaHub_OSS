package s3storage

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type Config struct {
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
	UseSSL    bool
}

type S3StorageProvider struct {
	client *minio.Client
	bucket string
}

func NewS3StorageProvider(cfg Config) (*S3StorageProvider, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("s3 endpoint is required")
	}
	if cfg.Bucket == "" {
		return nil, fmt.Errorf("s3 bucket is required")
	}
	if cfg.AccessKey == "" {
		return nil, fmt.Errorf("s3 access_key is required")
	}
	if cfg.SecretKey == "" {
		return nil, fmt.Errorf("s3 secret_key is required")
	}

	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create s3 client: %w", err)
	}

	return &S3StorageProvider{
		client: client,
		bucket: cfg.Bucket,
	}, nil
}

// Helper to construct main object key (e.g. "01HGFB9Z5W7ABCDEFGHJKMNPQR/10/10232")
func getObjectKey(dbID string, id int64) string {
	return fmt.Sprintf("%s/%d/%d", dbID, id/1000, id)
}

// Helper to construct preview object key (e.g. "previews/01HGFB9Z5W7ABCDEFGHJKMNPQR/10/10232")
func getPreviewObjectKey(dbID string, id int64) string {
	return fmt.Sprintf("previews/%s/%d/%d", dbID, id/1000, id)
}

func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	errResp := minio.ToErrorResponse(err)
	return errResp.Code == "NoSuchKey" || errResp.Code == "NotFound" || errResp.StatusCode == 404
}

// Helper function to inspect an io.Reader and determine size if possible.
// Returns -1 when the size cannot be determined safely so callers fall back to
// unknown-size streaming instead of silently truncating content.
func getStreamSize(r io.Reader) int64 {
	if f, ok := r.(*os.File); ok {
		// Trust Stat only for regular files (a pipe/FIFO reports size 0, which
		// would make minio upload an empty object and ignore the stream) and only
		// for the bytes that remain from the current offset.
		if stat, err := f.Stat(); err == nil && stat.Mode().IsRegular() {
			if current, err := f.Seek(0, io.SeekCurrent); err == nil {
				if remaining := stat.Size() - current; remaining >= 0 {
					return remaining
				}
				return 0
			}
		}
	}
	if b, ok := r.(*bytes.Buffer); ok {
		return int64(b.Len())
	}
	if b, ok := r.(*bytes.Reader); ok {
		return int64(b.Len())
	}
	if s, ok := r.(interface{ Len() int }); ok {
		return int64(s.Len())
	}
	if seeker, ok := r.(io.Seeker); ok {
		current, err := seeker.Seek(0, io.SeekCurrent)
		if err == nil {
			end, err := seeker.Seek(0, io.SeekEnd)
			if err == nil {
				seeker.Seek(current, io.SeekStart)
				return end - current
			}
			seeker.Seek(current, io.SeekStart)
		}
	}
	return -1
}
