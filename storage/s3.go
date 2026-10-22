package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

// S3Config holds configuration parameters for the S3-compatible storage driver.
type S3Config struct {
	// Bucket is the name of the target S3 bucket (required).
	Bucket string

	// Region is the AWS region (default: "us-east-1").
	Region string

	// Endpoint is an optional custom endpoint URL for MinIO, Cloudflare R2, LocalStack, or Ceph.
	Endpoint string

	// AccessKeyID and SecretAccessKey are optional static credentials.
	// If omitted, standard AWS credential providers (IAM, env vars, ~/.aws/credentials) are used.
	AccessKeyID     string
	SecretAccessKey string

	// UsePathStyle forces path-style addressing (e.g., http://endpoint/bucket/key), required by MinIO.
	UsePathStyle bool

	// Client is an optional pre-configured *s3.Client. If provided, credentials and endpoint options are bypassed.
	Client *s3.Client
}

// S3Storage provides a production-grade Storage implementation for Amazon S3,
// MinIO, Cloudflare R2, Google Cloud Storage, and other S3-compatible object stores.
type S3Storage struct {
	client        *s3.Client
	presignClient *s3.PresignClient
	bucket        string
}

// NewS3 initializes an S3Storage instance.
func NewS3(ctx context.Context, cfg S3Config) (*S3Storage, error) {
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, fmt.Errorf("storage: s3 bucket name is required")
	}

	region := cfg.Region
	if region == "" {
		region = "us-east-1"
	}

	client := cfg.Client
	if client == nil {
		var loadOpts []func(*awsconfig.LoadOptions) error
		loadOpts = append(loadOpts, awsconfig.WithRegion(region))

		if cfg.AccessKeyID != "" && cfg.SecretAccessKey != "" {
			loadOpts = append(loadOpts, awsconfig.WithCredentialsProvider(
				credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
			))
		}

		awsCfg, err := awsconfig.LoadDefaultConfig(ctx, loadOpts...)
		if err != nil {
			return nil, fmt.Errorf("storage: load aws config: %w", err)
		}

		client = s3.NewFromConfig(awsCfg, func(o *s3.Options) {
			if cfg.Endpoint != "" {
				o.BaseEndpoint = aws.String(cfg.Endpoint)
			}
			o.UsePathStyle = cfg.UsePathStyle
		})
	}

	return &S3Storage{
		client:        client,
		presignClient: s3.NewPresignClient(client),
		bucket:        cfg.Bucket,
	}, nil
}

// Put uploads an object to S3.
func (s *S3Storage) Put(ctx context.Context, key string, r io.Reader, opts ...PutOption) (*ObjectInfo, error) {
	if strings.TrimSpace(key) == "" {
		return nil, ErrInvalidKey
	}

	var options PutOptions
	for _, opt := range opts {
		opt(&options)
	}

	input := &s3.PutObjectInput{
		Bucket:   aws.String(s.bucket),
		Key:      aws.String(key),
		Body:     r,
		Metadata: options.Metadata,
	}

	if options.ContentType != "" {
		input.ContentType = aws.String(options.ContentType)
	}
	if options.ContentLength > 0 {
		input.ContentLength = aws.Int64(options.ContentLength)
	}

	out, err := s.client.PutObject(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("storage: s3 put object: %w", err)
	}

	etag := ""
	if out.ETag != nil {
		etag = strings.Trim(*out.ETag, `"`)
	}

	return &ObjectInfo{
		Key:          key,
		Size:         options.ContentLength,
		ContentType:  options.ContentType,
		ETag:         etag,
		LastModified: time.Now().UTC(),
		Metadata:     options.Metadata,
	}, nil
}

// Get downloads an object from S3.
func (s *S3Storage) Get(ctx context.Context, key string, opts ...GetOption) (io.ReadCloser, *ObjectInfo, error) {
	if strings.TrimSpace(key) == "" {
		return nil, nil, ErrInvalidKey
	}

	var options GetOptions
	for _, opt := range opts {
		opt(&options)
	}

	input := &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}

	if options.RangeStart != nil && options.RangeEnd != nil {
		start := *options.RangeStart
		end := *options.RangeEnd
		if start < 0 || end < start {
			return nil, nil, ErrInvalidRange
		}
		input.Range = aws.String(fmt.Sprintf("bytes=%d-%d", start, end))
	}

	out, err := s.client.GetObject(ctx, input)
	if err != nil {
		if isS3NotFound(err) {
			return nil, nil, ErrNotFound
		}
		return nil, nil, fmt.Errorf("storage: s3 get object: %w", err)
	}

	info := s.extractObjectInfo(key, out.ContentLength, out.ContentType, out.ETag, out.LastModified, out.Metadata)
	return out.Body, &info, nil
}

// Delete removes an object from S3.
func (s *S3Storage) Delete(ctx context.Context, key string) error {
	if strings.TrimSpace(key) == "" {
		return ErrInvalidKey
	}

	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isS3NotFound(err) {
			return nil
		}
		return fmt.Errorf("storage: s3 delete object: %w", err)
	}

	return nil
}

// DeleteMulti removes multiple objects in a single S3 batch request.
func (s *S3Storage) DeleteMulti(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}

	objectIDs := make([]types.ObjectIdentifier, 0, len(keys))
	for _, k := range keys {
		if strings.TrimSpace(k) != "" {
			objectIDs = append(objectIDs, types.ObjectIdentifier{Key: aws.String(k)})
		}
	}

	if len(objectIDs) == 0 {
		return nil
	}

	_, err := s.client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
		Bucket: aws.String(s.bucket),
		Delete: &types.Delete{
			Objects: objectIDs,
			Quiet:   aws.Bool(true),
		},
	})
	if err != nil {
		return fmt.Errorf("storage: s3 delete multi: %w", err)
	}

	return nil
}

// Exists checks if an object exists in S3 via HeadObject.
func (s *S3Storage) Exists(ctx context.Context, key string) (bool, error) {
	if strings.TrimSpace(key) == "" {
		return false, ErrInvalidKey
	}

	_, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err == nil {
		return true, nil
	}
	if isS3NotFound(err) {
		return false, nil
	}
	return false, fmt.Errorf("storage: s3 head object: %w", err)
}

// Stat retrieves object metadata without reading the payload.
func (s *S3Storage) Stat(ctx context.Context, key string) (*ObjectInfo, error) {
	if strings.TrimSpace(key) == "" {
		return nil, ErrInvalidKey
	}

	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		if isS3NotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: s3 head object: %w", err)
	}

	info := s.extractObjectInfo(key, out.ContentLength, out.ContentType, out.ETag, out.LastModified, out.Metadata)
	return &info, nil
}

// List queries objects matching prefix using S3 ListObjectsV2.
func (s *S3Storage) List(ctx context.Context, prefix string, opts ...ListOption) (*ListResult, error) {
	var options ListOptions
	for _, opt := range opts {
		opt(&options)
	}

	input := &s3.ListObjectsV2Input{
		Bucket: aws.String(s.bucket),
	}
	if prefix != "" {
		input.Prefix = aws.String(prefix)
	}
	if options.MaxKeys > 0 {
		maxKeys := options.MaxKeys
		if maxKeys > 1000 {
			maxKeys = 1000
		}
		// #nosec G115 -- maxKeys is guaranteed positive and bounded to <= 1000
		input.MaxKeys = aws.Int32(int32(maxKeys))
	}
	if options.ContinuationToken != "" {
		input.ContinuationToken = aws.String(options.ContinuationToken)
	}
	if options.Delimiter != "" {
		input.Delimiter = aws.String(options.Delimiter)
	}

	out, err := s.client.ListObjectsV2(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("storage: s3 list objects: %w", err)
	}

	isTruncated := false
	if out.IsTruncated != nil {
		isTruncated = *out.IsTruncated
	}

	res := &ListResult{
		Objects:        make([]ObjectInfo, 0, len(out.Contents)),
		CommonPrefixes: make([]string, 0, len(out.CommonPrefixes)),
		IsTruncated:    isTruncated,
	}

	if out.NextContinuationToken != nil {
		res.NextContinuationToken = *out.NextContinuationToken
	}

	for _, item := range out.Contents {
		etag := ""
		if item.ETag != nil {
			etag = strings.Trim(*item.ETag, `"`)
		}
		lastMod := time.Time{}
		if item.LastModified != nil {
			lastMod = *item.LastModified
		}
		var size int64
		if item.Size != nil {
			size = *item.Size
		}
		res.Objects = append(res.Objects, ObjectInfo{
			Key:          aws.ToString(item.Key),
			Size:         size,
			ETag:         etag,
			LastModified: lastMod,
		})
	}

	for _, cp := range out.CommonPrefixes {
		if cp.Prefix != nil {
			res.CommonPrefixes = append(res.CommonPrefixes, *cp.Prefix)
		}
	}

	return res, nil
}

// PresignGet generates an AWS SigV4 signed GET URL for downloading an object.
func (s *S3Storage) PresignGet(ctx context.Context, key string, ttl time.Duration, _ ...PresignOption) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", ErrInvalidKey
	}

	req, err := s.presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("storage: presign s3 get: %w", err)
	}

	return req.URL, nil
}

// PresignPut generates an AWS SigV4 signed PUT URL for uploading an object.
func (s *S3Storage) PresignPut(ctx context.Context, key string, ttl time.Duration, opts ...PresignOption) (string, error) {
	if strings.TrimSpace(key) == "" {
		return "", ErrInvalidKey
	}

	var options PresignOptions
	for _, opt := range opts {
		opt(&options)
	}

	input := &s3.PutObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}
	if options.ContentType != "" {
		input.ContentType = aws.String(options.ContentType)
	}

	req, err := s.presignClient.PresignPutObject(ctx, input, s3.WithPresignExpires(ttl))
	if err != nil {
		return "", fmt.Errorf("storage: presign s3 put: %w", err)
	}

	return req.URL, nil
}

func (s *S3Storage) extractObjectInfo(key string, contentLength *int64, contentType, etag *string, lastModified *time.Time, metadata map[string]string) ObjectInfo {
	var size int64
	if contentLength != nil {
		size = *contentLength
	}
	var ct string
	if contentType != nil {
		ct = *contentType
	}
	var et string
	if etag != nil {
		et = strings.Trim(*etag, `"`)
	}
	var lm time.Time
	if lastModified != nil {
		lm = *lastModified
	}

	return ObjectInfo{
		Key:          key,
		Size:         size,
		ContentType:  ct,
		ETag:         et,
		LastModified: lm,
		Metadata:     metadata,
	}
}

func isS3NotFound(err error) bool {
	if err == nil {
		return false
	}
	var notFound *types.NoSuchKey
	if errors.As(err, &notFound) {
		return true
	}
	var notFoundBucket *types.NoSuchBucket
	if errors.As(err, &notFoundBucket) {
		return true
	}
	var apiErr smithy.APIError
	if errors.As(err, &apiErr) {
		code := apiErr.ErrorCode()
		return code == "NoSuchKey" || code == "NotFound" || code == "NoSuchBucket"
	}
	return strings.Contains(err.Error(), "status code: 404") || strings.Contains(err.Error(), "NotFound")
}
