package storage

import (
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
)

type dummyAPIError struct {
	code string
}

func (d *dummyAPIError) Error() string                 { return d.code }
func (d *dummyAPIError) ErrorCode() string             { return d.code }
func (d *dummyAPIError) ErrorMessage() string          { return d.code }
func (d *dummyAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

func TestIsS3NotFound_TableDriven(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "NoSuchKey type",
			err:      &types.NoSuchKey{},
			expected: true,
		},
		{
			name:     "NoSuchBucket type",
			err:      &types.NoSuchBucket{},
			expected: true,
		},
		{
			name:     "APIError NoSuchKey",
			err:      &dummyAPIError{code: "NoSuchKey"},
			expected: true,
		},
		{
			name:     "APIError NotFound",
			err:      &dummyAPIError{code: "NotFound"},
			expected: true,
		},
		{
			name:     "APIError Other",
			err:      &dummyAPIError{code: "AccessDenied"},
			expected: false,
		},
		{
			name:     "strings contains status code 404",
			err:      errors.New("operation failed with status code: 404"),
			expected: true,
		},
		{
			name:     "random generic error",
			err:      errors.New("connection reset by peer"),
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isS3NotFound(tc.err)
			if got != tc.expected {
				t.Errorf("isS3NotFound(%v) = %v; want %v", tc.err, got, tc.expected)
			}
		})
	}
}

func TestS3ExtractObjectInfo_NilHandling(t *testing.T) {
	s := &S3Storage{}
	info := s.extractObjectInfo("nil-check", nil, nil, nil, nil, nil)
	if info.Size != 0 {
		t.Errorf("expected size 0, got %d", info.Size)
	}
	if info.ContentType != "" {
		t.Errorf("expected empty content type, got %s", info.ContentType)
	}
	if info.ETag != "" {
		t.Errorf("expected empty etag, got %s", info.ETag)
	}
	if !info.LastModified.IsZero() {
		t.Errorf("expected zero last modified, got %v", info.LastModified)
	}

	now := time.Now().UTC()
	info2 := s.extractObjectInfo("with-values", aws.Int64(42), aws.String("text/plain"), aws.String(`"quoted"`), &now, map[string]string{"env": "test"})
	if info2.Size != 42 || info2.ContentType != "text/plain" || info2.ETag != "quoted" || info2.Metadata["env"] != "test" {
		t.Errorf("unexpected extracted info: %+v", info2)
	}
}
