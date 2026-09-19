package misskey

import (
	"net/url"
	"strings"
	"testing"
)

func TestNewClientBuildsBoundedPrivateHTTPClient(t *testing.T) {
	baseURL, err := url.Parse("https://misskey.example.test")
	if err != nil {
		t.Fatalf("url.Parse: %v", err)
	}
	secret := "misskey-client-test-secret"

	client, err := NewClient(baseURL, secret)
	if err != nil {
		t.Fatalf("NewClient returned error: %v", err)
	}
	if client.httpClient == nil {
		t.Fatal("NewClient did not create an HTTP client")
	}
	if client.httpClient.Timeout != requestTimeout {
		t.Fatalf("HTTP timeout = %s, want %s", client.httpClient.Timeout, requestTimeout)
	}
	if client.token != secret {
		t.Fatal("client did not retain its token privately")
	}
}

func TestNewClientRejectsInvalidInputsWithoutEchoingValues(t *testing.T) {
	secret := "misskey-client-invalid-secret"
	tests := []struct {
		name    string
		baseURL *url.URL
		token   string
	}{
		{name: "nil URL", token: secret},
		{name: "missing token", baseURL: mustParseURL(t, "https://misskey.example.test")},
		{name: "query URL", baseURL: mustParseURL(t, "https://misskey.example.test?secret=value"), token: secret},
		{name: "userinfo URL", baseURL: mustParseURL(t, "https://user:password@misskey.example.test"), token: secret},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewClient(test.baseURL, test.token)
			if err == nil {
				t.Fatal("NewClient returned nil error")
			}
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error contains token: %q", err)
			}
		})
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", raw, err)
	}
	return u
}
