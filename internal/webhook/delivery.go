package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/getio0909/voice-asset-server/internal/platform/outboundhttp"
)

const (
	defaultDeliveryLease = 2 * time.Minute
	maxDeliveryBodyBytes = 64 * 1024
	maxDeliveryAttempts  = 5
	baseRetryDelay       = 5 * time.Second
	maxRetryDelay        = 5 * time.Minute
)

var (
	ErrNoDelivery = errors.New("no webhook delivery available")
	ErrLeaseLost  = errors.New("webhook delivery lease lost")
)

const (
	DeliveryErrorTimeout          = "timeout"
	DeliveryErrorTransport        = "transport"
	DeliveryErrorUnsafeEndpoint   = "unsafe_endpoint"
	DeliveryErrorConfiguration    = "configuration"
	DeliveryErrorHTTPClient       = "http_client_error"
	DeliveryErrorHTTPServer       = "http_server_error"
	DeliveryErrorResponseTooLarge = "response_too_large"
)

type DeliveryAttempt struct {
	Delivery
	EndpointURL      string
	SecretVersion    int64
	SecretCiphertext []byte
	LeaseOwner       string
}

type ClaimParams struct {
	WorkerID      string
	Now           time.Time
	LeaseDuration time.Duration
}

type CompleteParams struct {
	DeliveryID     string
	WorkerID       string
	Now            time.Time
	ResponseStatus int
}

type FailParams struct {
	DeliveryID     string
	WorkerID       string
	Now            time.Time
	RetryAt        time.Time
	ResponseStatus *int
	ErrorCode      string
	Retryable      bool
}

type DeliveryRepository interface {
	Claim(context.Context, ClaimParams) (DeliveryAttempt, error)
	Succeed(context.Context, CompleteParams) error
	Fail(context.Context, FailParams) error
}

type DeliveryWorker struct {
	repository DeliveryRepository
	cipher     SecretCipher
	client     *http.Client
	workerID   string
	now        func() time.Time
	lease      time.Duration
}

func NewDeliveryWorker(
	repository DeliveryRepository,
	cipher SecretCipher,
	client *http.Client,
	workerID string,
) *DeliveryWorker {
	if client == nil {
		client = outboundhttp.NewClient(nil)
	} else {
		client = outboundhttp.NewClient(client)
	}
	return &DeliveryWorker{
		repository: repository, cipher: cipher, client: client,
		workerID: workerID, now: time.Now, lease: defaultDeliveryLease,
	}
}

func (worker *DeliveryWorker) RunOnce(ctx context.Context) (bool, error) {
	if worker == nil || worker.repository == nil || worker.cipher == nil ||
		worker.client == nil || strings.TrimSpace(worker.workerID) == "" || worker.now == nil {
		return false, errors.New("webhook delivery worker is not configured")
	}
	now := worker.now().UTC()
	lease := worker.lease
	if lease <= 0 {
		lease = defaultDeliveryLease
	}
	attempt, err := worker.repository.Claim(ctx, ClaimParams{
		WorkerID: worker.workerID, Now: now, LeaseDuration: lease,
	})
	if errors.Is(err, ErrNoDelivery) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim webhook delivery: %w", err)
	}
	if err := worker.deliver(ctx, attempt, now); err != nil {
		return true, err
	}
	return true, nil
}

func (worker *DeliveryWorker) deliver(
	ctx context.Context,
	attempt DeliveryAttempt,
	now time.Time,
) error {
	endpoint, err := outboundhttp.ParsePublicHTTPSURL(attempt.EndpointURL)
	if err != nil {
		return worker.fail(ctx, attempt, now, nil, DeliveryErrorUnsafeEndpoint, false)
	}
	secret, err := worker.cipher.Open(
		attempt.SecretCiphertext,
		secretAssociatedData(attempt.WorkspaceID, attempt.WebhookID, attempt.SecretVersion),
	)
	if err != nil || len(secret) == 0 {
		return worker.fail(ctx, attempt, now, nil, DeliveryErrorConfiguration, false)
	}
	defer clear(secret)
	timestamp := strconv.FormatInt(now.Unix(), 10)
	signature := signPayload(secret, timestamp, attempt.Payload)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(attempt.Payload))
	if err != nil {
		return worker.fail(ctx, attempt, now, nil, DeliveryErrorUnsafeEndpoint, false)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-VoiceAsset-Delivery", attempt.ID)
	request.Header.Set("X-VoiceAsset-Event", attempt.EventType)
	request.Header.Set("X-VoiceAsset-Timestamp", timestamp)
	request.Header.Set("X-VoiceAsset-Signature", "v1="+signature)
	response, err := worker.client.Do(request)
	if err != nil {
		code := DeliveryErrorTransport
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			code = DeliveryErrorTimeout
		}
		return worker.fail(ctx, attempt, now, nil, code, true)
	}
	defer response.Body.Close()
	read, err := io.Copy(io.Discard, io.LimitReader(response.Body, maxDeliveryBodyBytes+1))
	if err != nil {
		return worker.fail(ctx, attempt, now, &response.StatusCode, DeliveryErrorTransport, true)
	}
	if read > maxDeliveryBodyBytes {
		return worker.fail(ctx, attempt, now, &response.StatusCode, DeliveryErrorResponseTooLarge, false)
	}
	if response.StatusCode >= 200 && response.StatusCode <= 299 {
		if err := worker.repository.Succeed(ctx, CompleteParams{
			DeliveryID: attempt.ID, WorkerID: attempt.LeaseOwner, Now: now,
			ResponseStatus: response.StatusCode,
		}); err != nil {
			return fmt.Errorf("mark webhook delivery succeeded: %w", err)
		}
		return nil
	}
	retryable := response.StatusCode == http.StatusRequestTimeout ||
		response.StatusCode == http.StatusTooEarly ||
		response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500
	errorCode := DeliveryErrorHTTPClient
	if response.StatusCode >= 500 {
		errorCode = DeliveryErrorHTTPServer
	}
	return worker.fail(ctx, attempt, now, &response.StatusCode, errorCode, retryable)
}

func (worker *DeliveryWorker) fail(
	ctx context.Context,
	attempt DeliveryAttempt,
	now time.Time,
	responseStatus *int,
	errorCode string,
	retryable bool,
) error {
	retryAt := now
	if retryable && attempt.Attempts < maxDeliveryAttempts {
		retryAt = now.Add(retryDelay(attempt.Attempts))
	}
	if err := worker.repository.Fail(ctx, FailParams{
		DeliveryID: attempt.ID, WorkerID: attempt.LeaseOwner, Now: now,
		RetryAt: retryAt, ResponseStatus: responseStatus, ErrorCode: errorCode,
		Retryable: retryable,
	}); err != nil {
		return fmt.Errorf("mark webhook delivery failed: %w", err)
	}
	return nil
}

func signPayload(secret []byte, timestamp string, payload []byte) string {
	hash := hmac.New(sha256.New, secret)
	_, _ = hash.Write([]byte(timestamp))
	_, _ = hash.Write([]byte("."))
	_, _ = hash.Write(payload)
	return hex.EncodeToString(hash.Sum(nil))
}

func retryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := baseRetryDelay
	for index := 1; index < attempt; index++ {
		if delay >= maxRetryDelay/2 {
			return maxRetryDelay
		}
		delay *= 2
	}
	if delay > maxRetryDelay {
		return maxRetryDelay
	}
	return delay
}
