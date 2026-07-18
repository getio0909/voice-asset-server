package storage

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// New creates the configured durable storage driver. S3 uses the AWS SDK v2
// adapter below while the protocol-independent create-only semantics remain in
// s3_core.go.
func New(config Config) (Driver, error) {
	if err := config.Validate(); err != nil {
		return nil, err
	}
	switch config.Backend {
	case BackendLocal:
		return NewLocal(config.LocalRoot)
	case BackendS3:
		return NewS3(config.S3)
	default:
		return nil, fmt.Errorf("%w: %q", ErrInvalidBackend, config.Backend)
	}
}

// NewS3 constructs the production S3-compatible SDK adapter. It never logs or
// returns credential values; callers receive only sanitized operation errors.
func NewS3(config S3Config) (*S3, error) {
	if err := (Config{Backend: BackendS3, S3: config}).Validate(); err != nil {
		return nil, err
	}

	loadOptions := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(config.Region),
	}
	if config.Endpoint != "" {
		loadOptions = append(loadOptions, awsconfig.WithBaseEndpoint(config.Endpoint))
	}
	if config.AccessKeyID != "" {
		loadOptions = append(loadOptions, awsconfig.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(config.AccessKeyID, config.SecretAccessKey, config.SessionToken),
		))
	}
	if config.CAFile != "" {
		client, err := s3HTTPClient(config.CAFile)
		if err != nil {
			return nil, err
		}
		loadOptions = append(loadOptions, awsconfig.WithHTTPClient(client))
	}
	awsConfig, err := awsconfig.LoadDefaultConfig(context.Background(), loadOptions...)
	if err != nil {
		return nil, errors.New("load S3 SDK configuration")
	}
	client := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.UsePathStyle = config.ForcePathStyle
		if config.Endpoint != "" {
			options.BaseEndpoint = aws.String(config.Endpoint)
		}
	})
	return newS3WithClient(config, &awsS3ObjectClient{client: client, bucket: config.Bucket})
}

type awsS3ObjectClient struct {
	client *s3.Client
	bucket string
}

func (client *awsS3ObjectClient) PutIfAbsent(ctx context.Context, key string, source io.ReadSeeker, size int64, digest string) (bool, error) {
	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return false, errors.New("rewind S3 upload")
	}
	_, err := client.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(client.bucket),
		Key:           aws.String(key),
		Body:          source,
		ContentLength: aws.Int64(size),
		IfNoneMatch:   aws.String("*"),
	})
	if err == nil {
		return true, nil
	}
	if isS3Status(err, http.StatusPreconditionFailed) || isS3Code(err, "PreconditionFailed", "ConditionalRequestConflict") {
		return false, nil
	}
	return false, sanitizeS3Error("put object", err)
}

func (client *awsS3ObjectClient) Get(ctx context.Context, key string) (s3GetResult, error) {
	result, err := client.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(client.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return s3GetResult{}, sanitizeS3Error("get object", err)
	}
	if result.Body == nil || result.ContentLength == nil || *result.ContentLength < 0 {
		if result.Body != nil {
			_ = result.Body.Close()
		}
		return s3GetResult{}, errors.New("S3 response is missing object metadata")
	}
	etag := ""
	if result.ETag != nil {
		etag = strings.TrimSpace(*result.ETag)
	}
	return s3GetResult{Body: result.Body, Size: *result.ContentLength, ETag: etag}, nil
}

func (client *awsS3ObjectClient) List(ctx context.Context, prefix, token string, limit int) (s3ListResult, error) {
	result, err := client.client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{
		Bucket:            aws.String(client.bucket),
		Prefix:            aws.String(prefix),
		ContinuationToken: optionalString(token),
		MaxKeys:           aws.Int32(int32(limit)),
	})
	if err != nil {
		return s3ListResult{}, sanitizeS3Error("list objects", err)
	}
	keys := make([]string, 0, len(result.Contents))
	for _, object := range result.Contents {
		if object.Key != nil {
			keys = append(keys, *object.Key)
		}
	}
	nextToken := ""
	if result.NextContinuationToken != nil {
		nextToken = *result.NextContinuationToken
	}
	return s3ListResult{Keys: keys, NextToken: nextToken}, nil
}

func (client *awsS3ObjectClient) DeleteIfMatch(ctx context.Context, key, etag string) error {
	_, err := client.client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket:  aws.String(client.bucket),
		Key:     aws.String(key),
		IfMatch: aws.String(etag),
	})
	if err == nil {
		return nil
	}
	if isS3Status(err, http.StatusNotFound) || isS3Code(err, "NoSuchKey", "NotFound") {
		return errS3NotFound
	}
	if isS3Status(err, http.StatusPreconditionFailed) || isS3Code(err, "PreconditionFailed", "ConditionalRequestConflict") {
		return errS3PreconditionFailed
	}
	return sanitizeS3Error("delete object", err)
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return aws.String(value)
}

func isS3Status(err error, status int) bool {
	var responseErr *smithyhttp.ResponseError
	return errors.As(err, &responseErr) && responseErr.HTTPStatusCode() == status
}

func isS3Code(err error, codes ...string) bool {
	var apiErr smithy.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	for _, code := range codes {
		if apiErr.ErrorCode() == code {
			return true
		}
	}
	return false
}

func sanitizeS3Error(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	if isS3Status(err, http.StatusNotFound) || isS3Code(err, "NoSuchKey", "NotFound", "NoSuchBucket") {
		return errS3NotFound
	}
	if isS3Status(err, http.StatusPreconditionFailed) || isS3Code(err, "PreconditionFailed", "ConditionalRequestConflict") {
		return errS3PreconditionFailed
	}
	return fmt.Errorf("S3 %s failed", operation)
}

func s3HTTPClient(caFile string) (*http.Client, error) {
	data, err := os.ReadFile(caFile)
	if err != nil {
		return nil, errors.New("read S3 CA file")
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(data) {
		return nil, errors.New("parse S3 CA file")
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("clone default HTTP transport")
	}
	transport = transport.Clone()
	if transport.TLSClientConfig != nil {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	} else {
		transport.TLSClientConfig = &tls.Config{}
	}
	transport.TLSClientConfig.RootCAs = pool
	transport.TLSClientConfig.MinVersion = tls.VersionTLS12
	return &http.Client{Transport: transport}, nil
}

var _ s3ObjectClient = (*awsS3ObjectClient)(nil)
