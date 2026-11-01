// Package storage selects the DocumentStore adapter (CCSRA_ADAPTER_STORAGE).
package storage

import (
	"context"

	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/adapters/portable/s3store"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/platform"
	"github.com/lindritP/masterprojekt/app/ccsra-v1/internal/ports"
)

// Stores are two views of the same bucket. Internal is used for direct calls
// and for URLs handed to services in the cluster; Public signs URLs for
// clients outside it. On a cloud target both use the same endpoint; on the
// local stack the public one is reachable from the host
// (CCSRA_STORAGE_PUBLIC_ENDPOINT).
type Stores struct {
	Name     string
	Internal ports.DocumentStore
	Public   ports.DocumentStore
}

// FromEnv reads CCSRA_ADAPTER_STORAGE and CCSRA_STORAGE_*.
func FromEnv(ctx context.Context, e *platform.Env, secrets ports.SecretProvider) (Stores, error) {
	name := e.Adapter("CCSRA_ADAPTER_STORAGE", "s3")
	cfg := s3store.Config{
		Endpoint:  e.String("CCSRA_STORAGE_ENDPOINT", ""),
		Region:    e.Required("CCSRA_STORAGE_REGION"),
		Bucket:    e.Required("CCSRA_STORAGE_BUCKET"),
		AccessKey: e.Required("CCSRA_STORAGE_ACCESS_KEY"),
		SecretKey: e.Secret(ctx, secrets, "CCSRA_STORAGE_SECRET_KEY"),
		PathStyle: e.Bool("CCSRA_STORAGE_PATH_STYLE", false),
	}
	public := cfg
	public.Endpoint = e.String("CCSRA_STORAGE_PUBLIC_ENDPOINT", cfg.Endpoint)
	if err := e.Err(); err != nil {
		return Stores{}, err
	}
	in, err := s3store.New(cfg)
	if err != nil {
		return Stores{}, err
	}
	out, err := s3store.New(public)
	if err != nil {
		return Stores{}, err
	}
	return Stores{Name: name, Internal: in, Public: out}, nil
}
