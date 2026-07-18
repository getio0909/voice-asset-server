package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestDeliveryWorkerSignsExactPayloadAndMarksSuccess(t *testing.T) {
	now := time.Unix(1_721_299_800, 0).UTC()
	repository := &fakeDeliveryRepository{attempt: DeliveryAttempt{
		Delivery: Delivery{
			ID: "40000000-0000-4000-8000-0000000000d1", WorkspaceID: webhookWorkspaceID,
			WebhookID: webhookEndpointID, WebhookVersion: 3, EventID: "event-1",
			EventType: EventJobSucceeded, Payload: []byte(`{"type":"job.succeeded"}`),
			Attempts: 1, MaxAttempts: 5,
		},
		EndpointURL: "https://hooks.example.com/events", SecretVersion: 2,
		SecretCiphertext: []byte("ciphertext"), LeaseOwner: "worker-a",
	}}
	cipher := &deliveryCipher{secret: []byte("test-signing-secret")}
	transport := &captureRoundTripper{response: &http.Response{
		StatusCode: http.StatusNoContent, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("accepted")),
	}}
	worker := NewDeliveryWorker(repository, cipher, &http.Client{Transport: transport}, "worker-a")
	worker.now = func() time.Time { return now }

	processed, err := worker.RunOnce(context.Background())
	if err != nil || !processed {
		t.Fatalf("RunOnce() = (%v, %v)", processed, err)
	}
	request := transport.request
	if request == nil {
		t.Fatal("delivery request was not sent")
	}
	body, err := io.ReadAll(request.Body)
	if err != nil || !bytes.Equal(body, repository.attempt.Payload) {
		t.Fatalf("request body = %q, error = %v", body, err)
	}
	if request.Header.Get("X-VoiceAsset-Delivery") != repository.attempt.ID ||
		request.Header.Get("X-VoiceAsset-Event") != EventJobSucceeded ||
		request.Header.Get("X-VoiceAsset-Timestamp") != "1721299800" {
		t.Fatalf("request headers = %v", request.Header)
	}
	wantMAC := hmac.New(sha256.New, []byte("test-signing-secret"))
	_, _ = wantMAC.Write([]byte("1721299800."))
	_, _ = wantMAC.Write(repository.attempt.Payload)
	wantSignature := "v1=" + hex.EncodeToString(wantMAC.Sum(nil))
	if request.Header.Get("X-VoiceAsset-Signature") != wantSignature {
		t.Fatalf("signature = %q, want %q", request.Header.Get("X-VoiceAsset-Signature"), wantSignature)
	}
	if repository.succeed.ResponseStatus != http.StatusNoContent || repository.succeed.DeliveryID != repository.attempt.ID {
		t.Fatalf("success params = %+v", repository.succeed)
	}
	if string(cipher.aad) != string(secretAssociatedData(webhookWorkspaceID, webhookEndpointID, 2)) {
		t.Fatalf("secret AAD = %q", cipher.aad)
	}
}

func TestDeliveryWorkerRetriesServerFailuresAndPermanentlyFailsClientErrors(t *testing.T) {
	for _, test := range []struct {
		name      string
		status    int
		retryable bool
		errorCode string
	}{
		{name: "server", status: http.StatusBadGateway, retryable: true, errorCode: DeliveryErrorHTTPServer},
		{name: "client", status: http.StatusUnprocessableEntity, retryable: false, errorCode: DeliveryErrorHTTPClient},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := time.Unix(1_721_299_800, 0).UTC()
			repository := &fakeDeliveryRepository{attempt: testDeliveryAttempt()}
			worker := NewDeliveryWorker(
				repository,
				&deliveryCipher{secret: []byte("secret")},
				&http.Client{Transport: &captureRoundTripper{response: &http.Response{
					StatusCode: test.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("not persisted")),
				}}},
				"worker-a",
			)
			worker.now = func() time.Time { return now }
			if processed, err := worker.RunOnce(context.Background()); err != nil || !processed {
				t.Fatalf("RunOnce() = (%v, %v)", processed, err)
			}
			if repository.fail.ErrorCode != test.errorCode || repository.fail.Retryable != test.retryable ||
				repository.fail.ResponseStatus == nil || *repository.fail.ResponseStatus != test.status {
				t.Fatalf("failure params = %+v", repository.fail)
			}
			if test.retryable && !repository.fail.RetryAt.Equal(now.Add(baseRetryDelay)) {
				t.Fatalf("retry time = %v, want %v", repository.fail.RetryAt, now.Add(baseRetryDelay))
			}
		})
	}
}

func TestDeliveryWorkerBoundsResponseBodyAndHandlesEmptyQueue(t *testing.T) {
	repository := &fakeDeliveryRepository{attempt: testDeliveryAttempt()}
	oversized := bytes.Repeat([]byte{'x'}, maxDeliveryBodyBytes+1)
	worker := NewDeliveryWorker(
		repository,
		&deliveryCipher{secret: []byte("secret")},
		&http.Client{Transport: &captureRoundTripper{response: &http.Response{
			StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(oversized)),
		}}},
		"worker-a",
	)
	if processed, err := worker.RunOnce(context.Background()); err != nil || !processed {
		t.Fatalf("oversized RunOnce() = (%v, %v)", processed, err)
	}
	if repository.fail.ErrorCode != DeliveryErrorResponseTooLarge || repository.fail.Retryable {
		t.Fatalf("oversized failure params = %+v", repository.fail)
	}

	empty := &fakeDeliveryRepository{claimErr: ErrNoDelivery}
	emptyWorker := NewDeliveryWorker(empty, &deliveryCipher{secret: []byte("secret")}, nil, "worker-a")
	processed, err := emptyWorker.RunOnce(context.Background())
	if err != nil || processed {
		t.Fatalf("empty RunOnce() = (%v, %v)", processed, err)
	}
}

func testDeliveryAttempt() DeliveryAttempt {
	return DeliveryAttempt{
		Delivery: Delivery{
			ID: "40000000-0000-4000-8000-0000000000d1", WorkspaceID: webhookWorkspaceID,
			WebhookID: webhookEndpointID, WebhookVersion: 1, EventID: "event-1",
			EventType: EventJobFailed, Payload: []byte(`{"type":"job.failed"}`), Attempts: 1, MaxAttempts: 5,
		},
		EndpointURL: "https://hooks.example.com/events", SecretVersion: 1,
		SecretCiphertext: []byte("ciphertext"), LeaseOwner: "worker-a",
	}
}

type fakeDeliveryRepository struct {
	attempt  DeliveryAttempt
	claimErr error
	succeed  CompleteParams
	fail     FailParams
}

func (repository *fakeDeliveryRepository) Claim(context.Context, ClaimParams) (DeliveryAttempt, error) {
	if repository.claimErr != nil {
		return DeliveryAttempt{}, repository.claimErr
	}
	return repository.attempt, nil
}

func (repository *fakeDeliveryRepository) Succeed(_ context.Context, params CompleteParams) error {
	repository.succeed = params
	return nil
}

func (repository *fakeDeliveryRepository) Fail(_ context.Context, params FailParams) error {
	repository.fail = params
	return nil
}

type deliveryCipher struct {
	secret []byte
	aad    []byte
}

func (cipher *deliveryCipher) Seal([]byte, []byte) ([]byte, error) { return []byte("ciphertext"), nil }

func (cipher *deliveryCipher) Open(_ []byte, aad []byte) ([]byte, error) {
	cipher.aad = append([]byte(nil), aad...)
	if len(cipher.secret) == 0 {
		return nil, errors.New("secret unavailable")
	}
	return append([]byte(nil), cipher.secret...), nil
}

type captureRoundTripper struct {
	request  *http.Request
	response *http.Response
}

func (transport *captureRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.request = request
	return transport.response, nil
}

var _ DeliveryRepository = (*fakeDeliveryRepository)(nil)
var _ SecretCipher = (*deliveryCipher)(nil)
var _ http.RoundTripper = (*captureRoundTripper)(nil)
