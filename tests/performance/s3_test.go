package performance_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getio0909/voice-asset-server/internal/storage"
)

const (
	s3PerformancePartCount = 4
	s3PerformancePartBytes = 256 << 10
	s3PerformanceMaxBytes  = s3PerformancePartCount * s3PerformancePartBytes
)

// TestRemoteS3Lifecycle is an explicit, bounded S3-compatible acceptance and
// performance probe. It is skipped unless the caller provides a disposable
// bucket and credentials; values are never included in test output.
func TestRemoteS3Lifecycle(t *testing.T) {
	if os.Getenv("VOICEASSET_S3_PERF") != "1" {
		t.Skip("set VOICEASSET_S3_PERF=1 for the isolated S3-compatible lifecycle test")
	}

	endpoint := requiredEnvironment(t, "VOICEASSET_S3_ENDPOINT")
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("parse VOICEASSET_S3_ENDPOINT: %v", err)
	}
	bucket := requiredEnvironment(t, "VOICEASSET_S3_BUCKET")
	accessKeyID := requiredEnvironment(t, "VOICEASSET_S3_ACCESS_KEY_ID")
	secretAccessKey := requiredEnvironment(t, "VOICEASSET_S3_SECRET_ACCESS_KEY")
	region := strings.TrimSpace(os.Getenv("VOICEASSET_S3_REGION"))
	if region == "" {
		region = "us-east-1"
	}
	prefix := strings.TrimSpace(os.Getenv("VOICEASSET_S3_PREFIX"))
	if prefix == "" {
		prefix = "voiceasset-performance"
	}
	tempRoot := t.TempDir()
	store, err := storage.NewS3(storage.S3Config{
		Endpoint:        endpoint,
		Region:          region,
		Bucket:          bucket,
		Prefix:          prefix,
		AccessKeyID:     accessKeyID,
		SecretAccessKey: secretAccessKey,
		ForcePathStyle:  true,
		TempRoot:        tempRoot,
	})
	if err != nil {
		t.Fatalf("initialize S3 adapter: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	assetID := fmt.Sprintf("s3-perf-asset-%d", time.Now().UnixNano())
	uploadID := fmt.Sprintf("s3-perf-upload-%d", time.Now().UnixNano())
	objectID := fmt.Sprintf("s3-perf-object-%d", time.Now().UnixNano())
	payload := bytes.Repeat([]byte("voiceasset-s3-performance-"), (s3PerformanceMaxBytes/26)+1)[:s3PerformanceMaxBytes]
	payloadHash := sha256.Sum256(payload)
	payloadDigest := hex.EncodeToString(payloadHash[:])
	partSize := s3PerformancePartBytes

	started := time.Now()
	parts := make([]storage.Part, s3PerformancePartCount)
	partErrors := make(chan error, s3PerformancePartCount)
	var workers sync.WaitGroup
	workers.Add(s3PerformancePartCount)
	for index := range s3PerformancePartCount {
		go func(index int) {
			defer workers.Done()
			start := index * partSize
			end := start + partSize
			part := payload[start:end]
			digest := sha256.Sum256(part)
			stored, putErr := store.PutPart(ctx, uploadID, index+1, bytes.NewReader(part), storage.PutPartOptions{
				ExpectedSize:   int64(len(part)),
				ExpectedSHA256: hex.EncodeToString(digest[:]),
				MaxBytes:       int64(partSize),
			})
			if putErr != nil {
				partErrors <- fmt.Errorf("put part %d: %w", index+1, putErr)
				return
			}
			parts[index] = stored
		}(index)
	}
	workers.Wait()
	close(partErrors)
	for partErr := range partErrors {
		t.Fatal(partErr)
	}

	refs := make([]storage.PartRef, len(parts))
	for index, part := range parts {
		refs[index] = part.Ref()
	}
	assembled, err := store.Assemble(ctx, assetID, uploadID, refs, storage.AssembleOptions{
		ExpectedSize:   int64(len(payload)),
		ExpectedSHA256: payloadDigest,
		MaxBytes:       int64(len(payload)),
	})
	if err != nil {
		_ = store.DeleteParts(context.Background(), uploadID)
		t.Fatalf("assemble S3 object: %v", err)
	}
	defer func() {
		_ = store.DeleteObject(context.Background(), assembled.Key, assembled.Size, assembled.SHA256)
		_ = store.DeleteParts(context.Background(), uploadID)
	}()

	snapshot, err := store.Open(ctx, assembled.Key)
	if err != nil {
		t.Fatalf("open assembled S3 object: %v", err)
	}
	opened, readErr := io.ReadAll(snapshot)
	closeErr := snapshot.Close()
	if readErr != nil {
		t.Fatalf("read assembled S3 object: %v", readErr)
	}
	if closeErr != nil {
		t.Fatalf("close assembled S3 object: %v", closeErr)
	}
	if !bytes.Equal(opened, payload) {
		t.Fatal("assembled S3 object bytes changed during remote round trip")
	}

	immutable, err := store.PutImmutable(ctx, assetID, objectID, storage.ObjectKindProviderRawResponse, bytes.NewReader(payload), int64(len(payload)))
	if err != nil {
		t.Fatalf("put immutable S3 object: %v", err)
	}
	if err := store.DeleteObject(ctx, immutable.Key, immutable.Size, immutable.SHA256); err != nil {
		t.Fatalf("delete immutable S3 object: %v", err)
	}

	elapsed := time.Since(started)
	throughput := float64(len(payload)) / elapsed.Seconds() / (1024 * 1024)
	t.Logf(
		"endpoint_host=%s bucket=%s parts=%d bytes=%d elapsed=%s throughput=%.1f MiB/s lifecycle=put/assemble/open/verify/delete",
		parsedEndpoint.Host, bucket, len(parts), len(payload), elapsed.Round(time.Millisecond), throughput,
	)
}
