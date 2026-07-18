package storage_test

import (
	"errors"
	"testing"

	"github.com/getio0909/voice-asset-server/internal/storage"
)

func TestStorageConfigAcceptsLocalAndSecureS3(t *testing.T) {
	tests := []storage.Config{
		{Backend: storage.BackendLocal, LocalRoot: "./var/objects"},
		{Backend: storage.BackendS3, S3: storage.S3Config{
			Endpoint: "https://objects.example.test", Region: "us-east-1",
			Bucket: "voiceasset-test", Prefix: "tenant/voiceasset",
			AccessKeyID: "fixture-access", SecretAccessKey: "fixture-secret",
			ForcePathStyle: true, CAFile: "./test-ca.pem", TempRoot: "./var/s3-temp",
		}},
		{Backend: storage.BackendS3, S3: storage.S3Config{
			Endpoint: "http://127.0.0.1:9000", Region: "us-east-1",
			Bucket: "voiceasset-test", ForcePathStyle: true, TempRoot: "./var/s3-temp",
		}},
	}
	for _, config := range tests {
		if err := config.Validate(); err != nil {
			t.Fatalf("Validate(%+v) error = %v", config, err)
		}
	}
}

func TestStorageConfigRejectsUnsafeOrIncompleteS3(t *testing.T) {
	tests := []struct {
		name   string
		config storage.S3Config
	}{
		{name: "plaintext remote endpoint", config: validS3Config("http://objects.example.test")},
		{name: "endpoint path", config: validS3Config("https://objects.example.test/api")},
		{name: "invalid bucket", config: withBucket(validS3Config("https://objects.example.test"), "VoiceAsset")},
		{name: "noncanonical prefix", config: withPrefix(validS3Config("https://objects.example.test"), "voice/../other")},
		{name: "dot prefix", config: withPrefix(validS3Config("https://objects.example.test"), ".")},
		{name: "partial credentials", config: withCredentials(validS3Config("https://objects.example.test"), "access", "")},
		{name: "CA over HTTP", config: withCA(validS3Config("http://localhost:9000"), "ca.pem")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := (storage.Config{Backend: storage.BackendS3, S3: test.config}).Validate()
			if !errors.Is(err, storage.ErrInvalidArgument) {
				t.Fatalf("Validate() error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func validS3Config(endpoint string) storage.S3Config {
	return storage.S3Config{
		Endpoint: endpoint, Region: "us-east-1", Bucket: "voiceasset-test", TempRoot: "./var/s3-temp",
	}
}

func withBucket(config storage.S3Config, bucket string) storage.S3Config {
	config.Bucket = bucket
	return config
}

func withPrefix(config storage.S3Config, prefix string) storage.S3Config {
	config.Prefix = prefix
	return config
}

func withCredentials(config storage.S3Config, accessKey, secretKey string) storage.S3Config {
	config.AccessKeyID = accessKey
	config.SecretAccessKey = secretKey
	return config
}

func withCA(config storage.S3Config, caFile string) storage.S3Config {
	config.CAFile = caFile
	return config
}
