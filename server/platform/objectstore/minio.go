package objectstore

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	profilesyncservice "github.com/zerlinpi/Ant-Browser/server/services/profile-sync-service"
)

type Config struct {
	Endpoint   string
	Bucket     string
	AccessKey  string
	SecretKey  string
	Region     string
	AutoCreate bool
}

type Store struct {
	client *minio.Client
	bucket string
	region string
}

// MetadataStore is an explicit development-only object store. It preserves the
// profile synchronization protocol without accepting production credentials or
// pretending that encrypted objects were durably uploaded.
type MetadataStore struct {
	profilesyncservice.MetadataVerifier
}

func (MetadataStore) Ping(context.Context) error { return nil }
func (MetadataStore) Close() error               { return nil }

func Open(ctx context.Context, config Config) (*Store, error) {
	parsed, err := url.Parse(strings.TrimSpace(config.Endpoint))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("object store endpoint must be an HTTP(S) origin without a path")
	}
	bucket := strings.TrimSpace(config.Bucket)
	if bucket == "" || strings.TrimSpace(config.AccessKey) == "" || strings.TrimSpace(config.SecretKey) == "" {
		return nil, errors.New("object store bucket and credentials are required")
	}
	client, err := minio.New(parsed.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(strings.TrimSpace(config.AccessKey), strings.TrimSpace(config.SecretKey), ""),
		Secure: parsed.Scheme == "https", Region: strings.TrimSpace(config.Region),
	})
	if err != nil {
		return nil, fmt.Errorf("create object store client: %w", err)
	}
	store := &Store{client: client, bucket: bucket, region: strings.TrimSpace(config.Region)}
	exists, err := client.BucketExists(ctx, bucket)
	if err != nil {
		return nil, fmt.Errorf("check object store bucket: %w", err)
	}
	if !exists {
		if !config.AutoCreate {
			return nil, fmt.Errorf("object store bucket %q does not exist", bucket)
		}
		if err := client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: store.region}); err != nil {
			exists, checkErr := client.BucketExists(ctx, bucket)
			if checkErr != nil || !exists {
				return nil, fmt.Errorf("create object store bucket: %w", err)
			}
		}
	}
	return store, nil
}

func (s *Store) Ping(ctx context.Context) error {
	exists, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("object store bucket is unavailable")
	}
	return nil
}

func (s *Store) PresignUpload(ctx context.Context, object profilesyncservice.Object, ttl time.Duration) (string, error) {
	if object.ObjectKey == "" || ttl <= 0 || ttl > time.Hour {
		return "", errors.New("object upload request is invalid")
	}
	presigned, err := s.client.PresignedPutObject(ctx, s.bucket, object.ObjectKey, ttl)
	if err != nil {
		return "", err
	}
	return presigned.String(), nil
}

func (s *Store) PresignDownload(ctx context.Context, object profilesyncservice.Object, ttl time.Duration) (string, error) {
	if object.ObjectKey == "" || ttl <= 0 || ttl > time.Hour {
		return "", errors.New("object download request is invalid")
	}
	presigned, err := s.client.PresignedGetObject(ctx, s.bucket, object.ObjectKey, ttl, url.Values{})
	if err != nil {
		return "", err
	}
	return presigned.String(), nil
}

func (s *Store) Verify(ctx context.Context, expected profilesyncservice.Object) error {
	if expected.ObjectKey == "" || expected.SizeBytes < 0 || len(expected.ContentHash) != sha256.Size*2 {
		return profilesyncservice.ErrObjectUnavailable
	}
	info, err := s.client.StatObject(ctx, s.bucket, expected.ObjectKey, minio.StatObjectOptions{})
	if err != nil || info.Size != expected.SizeBytes {
		return profilesyncservice.ErrObjectUnavailable
	}
	object, err := s.client.GetObject(ctx, s.bucket, expected.ObjectKey, minio.GetObjectOptions{})
	if err != nil {
		return profilesyncservice.ErrObjectUnavailable
	}
	defer object.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(object, expected.SizeBytes+1))
	if err != nil || written != expected.SizeBytes {
		return profilesyncservice.ErrObjectUnavailable
	}
	expectedHash, err := hex.DecodeString(expected.ContentHash)
	if err != nil || subtle.ConstantTimeCompare(hash.Sum(nil), expectedHash) != 1 {
		return profilesyncservice.ErrObjectUnavailable
	}
	return nil
}

func (s *Store) Close() error { return nil }
