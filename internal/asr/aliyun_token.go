package asr

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" // #nosec G505 -- required by the documented Alibaba POP v1 signature protocol.
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

const aliyunTokenEndpoint = "https://nls-meta.cn-shanghai.aliyuncs.com/"

type aliyunTokenSource interface {
	Token(ctx context.Context) (string, error)
}

type aliyunStaticTokenSource struct{ token string }

func (source aliyunStaticTokenSource) Token(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !validOpaqueCredential(source.token) {
		return "", newProviderError("aliyun_asr", "token", ErrorInvalidConfiguration, "invalid_token", nil)
	}
	return source.token, nil
}

type aliyunAccessKeyTokenSource struct {
	accessKeyID     string
	accessKeySecret string
	client          *http.Client
	endpoint        string
	now             func() time.Time
	random          io.Reader

	mu        sync.Mutex
	token     string
	expiresAt time.Time
}

func newAliyunAccessKeyTokenSource(
	accessKeyID,
	accessKeySecret string,
	client *http.Client,
) (*aliyunAccessKeyTokenSource, error) {
	if !validOpaqueCredential(accessKeyID) || !validOpaqueCredential(accessKeySecret) {
		return nil, newProviderError(
			"aliyun_asr", "configure", ErrorInvalidConfiguration, "invalid_access_key", nil,
		)
	}
	return &aliyunAccessKeyTokenSource{
		accessKeyID: accessKeyID, accessKeySecret: accessKeySecret,
		client: providerHTTPClient(client), endpoint: aliyunTokenEndpoint,
		now: time.Now, random: rand.Reader,
	}, nil
}

func (source *aliyunAccessKeyTokenSource) Token(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	source.mu.Lock()
	defer source.mu.Unlock()
	now := source.now().UTC()
	if source.token != "" && source.expiresAt.After(now.Add(5*time.Minute)) {
		return source.token, nil
	}

	nonce, err := aliyunNonce(source.random)
	if err != nil {
		return "", newProviderError("aliyun_asr", "token", ErrorTransient, "nonce_failed", err)
	}
	parameters := map[string]string{
		"AccessKeyId":      source.accessKeyID,
		"Action":           "CreateToken",
		"Format":           "JSON",
		"RegionId":         "cn-shanghai",
		"SignatureMethod":  "HMAC-SHA1",
		"SignatureNonce":   nonce,
		"SignatureVersion": "1.0",
		"Timestamp":        now.Format("2006-01-02T15:04:05Z"),
		"Version":          "2019-02-28",
	}
	canonical := aliyunCanonicalQuery(parameters)
	signature := aliyunSignature("GET", "/", canonical, source.accessKeySecret)
	requestURL, err := url.Parse(source.endpoint)
	if err != nil {
		return "", newProviderError("aliyun_asr", "token", ErrorInvalidConfiguration, "invalid_endpoint", err)
	}
	requestURL.RawQuery = "Signature=" + aliyunPercentEncode(signature) + "&" + canonical
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return "", newProviderError("aliyun_asr", "token", ErrorInvalidConfiguration, "invalid_request", err)
	}
	request.Header.Set("Accept", "application/json")
	response, err := source.client.Do(request)
	if err != nil {
		return "", newProviderError("aliyun_asr", "token", ErrorTransient, "transport", err)
	}
	data, readErr := readProviderResponse(response.Body)
	if readErr != nil {
		return "", newProviderError("aliyun_asr", "token", ErrorTransient, "response_read", readErr)
	}
	if response.StatusCode != http.StatusOK {
		return "", httpStatusError("aliyun_asr", "token", response)
	}
	var envelope struct {
		Token struct {
			ID         string `json:"Id"`
			ExpireTime int64  `json:"ExpireTime"`
		} `json:"Token"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil ||
		!validOpaqueCredential(envelope.Token.ID) || envelope.Token.ExpireTime <= now.Unix() {
		return "", newProviderError("aliyun_asr", "token", ErrorRejected, "invalid_response", err)
	}
	source.token = envelope.Token.ID
	source.expiresAt = time.Unix(envelope.Token.ExpireTime, 0).UTC()
	return source.token, nil
}

func aliyunCanonicalQuery(parameters map[string]string) string {
	keys := make([]string, 0, len(parameters))
	for key := range parameters {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, aliyunPercentEncode(key)+"="+aliyunPercentEncode(parameters[key]))
	}
	return strings.Join(parts, "&")
}

func aliyunSignature(method, path, canonical, secret string) string {
	stringToSign := method + "&" + aliyunPercentEncode(path) + "&" + aliyunPercentEncode(canonical)
	mac := hmac.New(sha1.New, []byte(secret+"&"))
	_, _ = mac.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func aliyunPercentEncode(value string) string {
	encoded := url.QueryEscape(value)
	encoded = strings.ReplaceAll(encoded, "+", "%20")
	encoded = strings.ReplaceAll(encoded, "%7E", "~")
	encoded = strings.ReplaceAll(encoded, "%7e", "~")
	return encoded
}

func aliyunNonce(source io.Reader) (string, error) {
	if source == nil {
		return "", errors.New("random source is nil")
	}
	buffer := make([]byte, 16)
	if _, err := io.ReadFull(source, buffer); err != nil {
		return "", err
	}
	buffer[6] = (buffer[6] & 0x0f) | 0x40
	buffer[8] = (buffer[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(buffer)
	return fmt.Sprintf("%s-%s-%s-%s-%s", encoded[0:8], encoded[8:12], encoded[12:16], encoded[16:20], encoded[20:32]), nil
}

func validOpaqueCredential(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) < 8 || len(value) > 4096 {
		return false
	}
	for _, character := range value {
		if character <= 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}
