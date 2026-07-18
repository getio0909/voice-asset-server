package asr

import (
	"errors"
	"strings"
	"testing"
)

func TestConfiguredProviderFactoryBuildsSupportedAdaptersWithoutNetwork(t *testing.T) {
	aliyun := DefaultAliyunFlashProfile()
	aliyun.ID = "aliyun-profile"
	aliyun.VendorExtension = []byte(`{"appkey":"fixture-appkey"}`)
	tencent := DefaultTencentFlashProfile("1234567890")
	tencent.ID = "tencent-profile"
	mock := DefaultMockProfile("mock-profile")

	tests := []struct {
		name       string
		profile    Profile
		secret     []byte
		providerID string
	}{
		{name: "mock", profile: mock, providerID: MockProviderID},
		{
			name: "Aliyun static token", profile: aliyun,
			secret: []byte(`{"access_token":"fixture-token"}`), providerID: AliyunProviderID,
		},
		{
			name: "Tencent", profile: tencent,
			secret:     []byte(`{"secret_id":"fixture-secret-id","secret_key":"fixture-secret-key"}`),
			providerID: TencentProviderID,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider, err := NewConfiguredProvider(test.profile, test.secret, nil)
			if err != nil {
				t.Fatalf("NewConfiguredProvider() error = %v", err)
			}
			if provider.ID() != test.providerID {
				t.Fatalf("provider ID = %q", provider.ID())
			}
		})
	}
}

func TestConfiguredProviderFactoryRejectsUnknownFieldsAndNeverLeaksValues(t *testing.T) {
	profile := DefaultTencentFlashProfile("1234567890")
	profile.ID = "tencent-profile"
	const secretValue = "fixture-secret-that-must-not-leak"
	_, err := NewConfiguredProvider(
		profile,
		[]byte(`{"secret_id":"fixture-secret-id","secret_key":"`+secretValue+`","extra":"value"}`),
		nil,
	)
	if ErrorClassOf(err) != ErrorInvalidConfiguration || strings.Contains(err.Error(), secretValue) {
		t.Fatalf("factory error = %v", err)
	}
}

func TestConfiguredProviderFactoryRejectsProviderMismatchAndMockSecret(t *testing.T) {
	profile := DefaultMockProfile("mock-profile")
	profile.ProviderID = "unknown"
	if _, err := NewConfiguredProvider(profile, nil, nil); ErrorClassOf(err) != ErrorUnsupported {
		t.Fatalf("unknown provider error = %v", err)
	}

	profile = DefaultMockProfile("mock-profile")
	if _, err := NewConfiguredProvider(profile, []byte(`{"token":"unexpected"}`), nil); ErrorClassOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("mock secret error = %v", err)
	}

	invalid := DefaultAliyunFlashProfile()
	invalid.ID = "aliyun-profile"
	invalid.Endpoint = "http://127.0.0.1"
	invalid.VendorExtension = []byte(`{"appkey":"fixture-appkey"}`)
	if err := ValidateProfileDefinition(invalid); !errors.Is(err, ErrInvalidProfile) {
		t.Fatalf("invalid profile error = %v", err)
	}
}
