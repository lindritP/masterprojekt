// Package s3store is the portable DocumentStore adapter (SRS §7.3): the S3
// API against a configurable endpoint, which covers Amazon S3 and the
// S3-compatible stores of EU providers and of the local stack.
//
// Uploads are presigned POST forms, because only a POST policy can make the
// store itself enforce a maximum size (FR-302). Retention uses S3 Object Lock
// in compliance mode; a store or bucket without it yields ErrUnsupported.
package s3store

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awshttp "github.com/aws/aws-sdk-go-v2/aws/transport/http"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports"
)

// Config selects the endpoint and bucket. Credentials are static keys, which
// is what S3-compatible providers issue.
type Config struct {
	Endpoint  string // CCSRA_STORAGE_ENDPOINT; empty means the provider default
	Region    string
	Bucket    string // CCSRA_STORAGE_BUCKET
	AccessKey string
	SecretKey string
	PathStyle bool // most S3-compatible stores need path-style addressing
}

// Store implements ports.DocumentStore.
type Store struct {
	client  *s3.Client
	presign *s3.PresignClient
	bucket  string
}

var _ ports.DocumentStore = (*Store)(nil)

func New(cfg Config) (*Store, error) {
	if cfg.Bucket == "" || cfg.Region == "" {
		return nil, errors.New("s3store: bucket and region are required")
	}
	opts := s3.Options{
		Region:       cfg.Region,
		UsePathStyle: cfg.PathStyle,
		Credentials:  credentials.NewStaticCredentialsProvider(cfg.AccessKey, cfg.SecretKey, ""),
	}
	if cfg.Endpoint != "" {
		opts.BaseEndpoint = aws.String(cfg.Endpoint)
	}
	client := s3.New(opts)
	return &Store{client: client, presign: s3.NewPresignClient(client), bucket: cfg.Bucket}, nil
}

func (s *Store) PresignUpload(ctx context.Context, key string, c ports.PutConstraints) (ports.PresignedURL, error) {
	post, err := s.presign.PresignPostObject(ctx, &s3.PutObjectInput{Bucket: &s.bucket, Key: &key}, func(o *s3.PresignPostOptions) {
		o.Expires = c.TTL
		o.Conditions = []any{
			[]any{"content-length-range", 1, c.MaxSizeBytes},
			map[string]string{"Content-Type": c.ContentType},
		}
	})
	if err != nil {
		return ports.PresignedURL{}, fmt.Errorf("presign upload %s: %w", key, err)
	}
	fields := map[string]string{"Content-Type": c.ContentType}
	maps.Copy(fields, post.Values)
	return ports.PresignedURL{URL: post.URL, Method: http.MethodPost, FormFields: fields, ExpiresAt: time.Now().Add(c.TTL)}, nil
}

func (s *Store) PresignGet(ctx context.Context, key string, ttl time.Duration) (ports.PresignedURL, error) {
	req, err := s.presign.PresignGetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &key}, s3.WithPresignExpires(ttl))
	if err != nil {
		return ports.PresignedURL{}, fmt.Errorf("presign get %s: %w", key, err)
	}
	return ports.PresignedURL{URL: req.URL, Method: http.MethodGet, ExpiresAt: time.Now().Add(ttl)}, nil
}

func (s *Store) Head(ctx context.Context, key string) (ports.ObjectInfo, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.bucket, Key: &key})
	if isNotFound(err) {
		return ports.ObjectInfo{}, fmt.Errorf("head %s: %w", key, ports.ErrNotFound)
	}
	if err != nil {
		return ports.ObjectInfo{}, fmt.Errorf("head %s: %w", key, err)
	}
	return ports.ObjectInfo{
		Key:       key,
		SizeBytes: aws.ToInt64(out.ContentLength),
		ETag:      aws.ToString(out.ETag),
		UpdatedAt: aws.ToTime(out.LastModified),
	}, nil
}

// Delete removes the current version of the object, so that a version under
// retention makes the call fail instead of hiding behind a delete marker.
func (s *Store) Delete(ctx context.Context, key string) error {
	head, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.bucket, Key: &key})
	if isNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("delete %s: %w", key, err)
	}
	if _, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &s.bucket, Key: &key, VersionId: head.VersionId}); err != nil {
		return fmt.Errorf("delete %s: %w", key, err)
	}
	return nil
}

func (s *Store) ApplyRetention(ctx context.Context, key string, until time.Time) error {
	_, err := s.client.PutObjectRetention(ctx, &s3.PutObjectRetentionInput{
		Bucket:    &s.bucket,
		Key:       &key,
		Retention: &types.ObjectLockRetention{Mode: types.ObjectLockRetentionModeCompliance, RetainUntilDate: &until},
	})
	if err == nil {
		return nil
	}
	if apiErr, ok := errors.AsType[smithy.APIError](err); ok {
		switch apiErr.ErrorCode() {
		case "NotImplemented", "ObjectLockConfigurationNotFoundError", "InvalidRequest":
			return fmt.Errorf("retention on %s: %s: %w", key, apiErr.ErrorCode(), ports.ErrUnsupported)
		}
	}
	return fmt.Errorf("retention on %s: %w", key, err)
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	if _, ok := errors.AsType[*types.NotFound](err); ok {
		return true
	}
	if _, ok := errors.AsType[*types.NoSuchKey](err); ok {
		return true
	}
	if re, ok := errors.AsType[*awshttp.ResponseError](err); ok {
		return re.HTTPStatusCode() == http.StatusNotFound
	}
	return false
}
