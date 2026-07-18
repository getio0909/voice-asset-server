package storage

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"strings"
)

var bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)

// Config selects one durable storage driver. S3 credentials may be omitted to
// use the SDK's default credential chain, but partial static credentials are
// rejected.
type Config struct {
	Backend   Backend
	LocalRoot string
	S3        S3Config
}

type S3Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	Prefix          string
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	ForcePathStyle  bool
	CAFile          string
	TempRoot        string
}

func (config Config) Validate() error {
	if !config.Backend.Valid() {
		return fmt.Errorf("%w: %q", ErrInvalidBackend, config.Backend)
	}
	if config.Backend == BackendLocal {
		if strings.TrimSpace(config.LocalRoot) == "" {
			return fmt.Errorf("%w: local storage root is empty", ErrInvalidArgument)
		}
		return nil
	}
	return config.S3.validate()
}

func (config S3Config) validate() error {
	endpoint, err := validateS3Endpoint(config.Endpoint)
	if err != nil {
		return err
	}
	if strings.TrimSpace(config.Region) == "" {
		return fmt.Errorf("%w: S3 region is empty", ErrInvalidArgument)
	}
	if !validBucket(config.Bucket) {
		return fmt.Errorf("%w: S3 bucket is invalid", ErrInvalidArgument)
	}
	if err := validatePrefix(config.Prefix); err != nil {
		return err
	}
	hasAccessKey := strings.TrimSpace(config.AccessKeyID) != ""
	hasSecretKey := strings.TrimSpace(config.SecretAccessKey) != ""
	if hasAccessKey != hasSecretKey || (strings.TrimSpace(config.SessionToken) != "" && !hasAccessKey) {
		return fmt.Errorf("%w: S3 static credentials are incomplete", ErrInvalidArgument)
	}
	if strings.TrimSpace(config.TempRoot) == "" {
		return fmt.Errorf("%w: S3 temporary root is empty", ErrInvalidArgument)
	}
	if endpoint != nil && endpoint.Scheme != "https" && strings.TrimSpace(config.CAFile) != "" {
		return fmt.Errorf("%w: an S3 CA file requires HTTPS", ErrInvalidArgument)
	}
	return nil
}

func validateS3Endpoint(value string) (*url.URL, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	endpoint, err := url.Parse(value)
	if err != nil || endpoint.Host == "" || endpoint.User != nil ||
		(endpoint.Scheme != "http" && endpoint.Scheme != "https") ||
		(endpoint.Path != "" && endpoint.Path != "/") || endpoint.RawQuery != "" || endpoint.Fragment != "" {
		return nil, fmt.Errorf("%w: S3 endpoint must be an HTTP(S) origin", ErrInvalidArgument)
	}
	if endpoint.Scheme == "http" && !isLoopbackHost(endpoint.Hostname()) {
		return nil, fmt.Errorf("%w: plaintext S3 endpoints are limited to loopback development", ErrInvalidArgument)
	}
	return endpoint, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func validBucket(bucket string) bool {
	if strings.TrimSpace(bucket) != bucket || !bucketPattern.MatchString(bucket) ||
		strings.Contains(bucket, "..") || net.ParseIP(bucket) != nil {
		return false
	}
	for _, label := range strings.Split(bucket, ".") {
		if strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
	}
	return true
}

func validatePrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if strings.TrimSpace(prefix) != prefix || strings.ContainsRune(prefix, '\x00') ||
		strings.Contains(prefix, `\`) || strings.HasPrefix(prefix, "/") ||
		strings.HasSuffix(prefix, "/") || prefix == "." || prefix == ".." ||
		strings.HasPrefix(prefix, "../") || path.Clean(prefix) != prefix {
		return fmt.Errorf("%w: S3 prefix must be a canonical relative path", ErrInvalidArgument)
	}
	return nil
}
