package spool

import (
	"context"
	"fmt"
	"io"
	"net/url"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// R2 reads the spool bucket through R2's S3-compatible API.
type R2 struct {
	client *minio.Client
	bucket string
}

// NewR2 connects to endpoint (https://<account>.r2.cloudflarestorage.com).
func NewR2(endpoint, bucket, accessKeyID, secretAccessKey string) (*R2, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("invalid R2 endpoint %q", endpoint)
	}
	client, err := minio.New(u.Host, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKeyID, secretAccessKey, ""),
		Secure: u.Scheme != "http",
		Region: "auto",
	})
	if err != nil {
		return nil, err
	}
	return &R2{client: client, bucket: bucket}, nil
}

func (r *R2) List(ctx context.Context, startAfter string) ([]string, error) {
	var keys []string
	for obj := range r.client.ListObjects(ctx, r.bucket, minio.ListObjectsOptions{
		StartAfter: startAfter,
		Recursive:  true,
	}) {
		if obj.Err != nil {
			return nil, obj.Err
		}
		keys = append(keys, obj.Key)
	}
	return keys, nil
}

func (r *R2) Get(ctx context.Context, key string) (io.ReadCloser, Object, error) {
	obj, err := r.client.GetObject(ctx, r.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, Object{}, err
	}
	info, err := obj.Stat()
	if err != nil {
		obj.Close()
		return nil, Object{}, err
	}
	return obj, Object{
		Key:          key,
		Size:         info.Size,
		EnvelopeFrom: metaLookup(info.UserMetadata, MetaEnvelopeFrom),
		EnvelopeTo:   metaLookup(info.UserMetadata, MetaEnvelopeTo),
	}, nil
}
