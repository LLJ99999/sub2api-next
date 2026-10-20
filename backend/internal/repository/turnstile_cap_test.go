package repository

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseCapSecret(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		isCap   bool
		wantErr bool
		want    capSecretConfig
	}{
		{name: "turnstile secret untouched", raw: "0x4AAAAAAAsecret", isCap: false},
		{name: "empty", raw: "", isCap: false},
		{name: "key and secret", raw: "cap:abc123:s3cr3t", isCap: true, want: capSecretConfig{SiteKey: "abc123", Secret: "s3cr3t"}},
		{name: "secret containing colon", raw: "cap:abc:se:cret", isCap: true, want: capSecretConfig{SiteKey: "abc", Secret: "se:cret"}},
		{name: "explicit base", raw: "cap:https://cap.example.com/|abc:sec", isCap: true, want: capSecretConfig{BaseURL: "https://cap.example.com", SiteKey: "abc", Secret: "sec"}},
		{name: "trim spaces", raw: "  cap:abc:sec  ", isCap: true, want: capSecretConfig{SiteKey: "abc", Secret: "sec"}},
		{name: "missing secret", raw: "cap:abc", isCap: true, wantErr: true},
		{name: "empty key", raw: "cap::sec", isCap: true, wantErr: true},
		{name: "bad base", raw: "cap:ftp://x|abc:sec", isCap: true, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, isCap, err := parseCapSecret(tc.raw)
			require.Equal(t, tc.isCap, isCap)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestResolveCapBaseURL(t *testing.T) {
	env := func(m map[string]string) func(string) string {
		return func(k string) string { return m[k] }
	}
	b, err := resolveCapBaseURL("https://x.example/cap/", env(nil))
	require.NoError(t, err)
	require.Equal(t, "https://x.example/cap", b)

	b, err = resolveCapBaseURL("", env(map[string]string{"CAP_BASE_URL": "http://cap:3000/", "SERVER_FRONTEND_URL": "https://site"}))
	require.NoError(t, err)
	require.Equal(t, "http://cap:3000", b)

	b, err = resolveCapBaseURL("", env(map[string]string{"SERVER_FRONTEND_URL": "https://site.example/"}))
	require.NoError(t, err)
	require.Equal(t, "https://site.example/cap", b)

	_, err = resolveCapBaseURL("", env(nil))
	require.Error(t, err)
}

func newCapTestVerifier(t *testing.T, base string) *turnstileVerifier {
	t.Helper()
	v, ok := NewTurnstileVerifier().(*turnstileVerifier)
	require.True(t, ok)
	v.capBaseURL = base
	// Cloudflare 路径不得被调用
	v.verifyURL = "http://127.0.0.1:1/should-not-be-called"
	return v
}

func TestTurnstileVerifier_CapSiteverify_Success(t *testing.T) {
	var gotPath, gotCT string
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotCT = r.Header.Get("Content-Type")
		require.Equal(t, http.MethodPost, r.Method)
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()

	v := newCapTestVerifier(t, srv.URL+"/cap")
	resp, err := v.VerifyToken(context.Background(), "cap:site1:sec1", "tok-xyz", "1.2.3.4")
	require.NoError(t, err)
	require.True(t, resp.Success)
	require.Equal(t, "/cap/site1/siteverify", gotPath)
	require.Equal(t, "application/json", gotCT)
	require.Equal(t, map[string]string{"secret": "sec1", "response": "tok-xyz"}, gotBody)
}

func TestTurnstileVerifier_CapSiteverify_ExplicitBaseInSecret(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()

	v := newCapTestVerifier(t, "http://127.0.0.1:1/unused")
	resp, err := v.VerifyToken(context.Background(), "cap:"+srv.URL+"|k2:s2", "tok", "")
	require.NoError(t, err)
	require.True(t, resp.Success)
	require.Equal(t, "/k2/siteverify", gotPath)
}

func TestTurnstileVerifier_CapSiteverify_Failure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"success":false,"error":"Invalid token"}`))
	}))
	defer srv.Close()

	v := newCapTestVerifier(t, srv.URL)
	resp, err := v.VerifyToken(context.Background(), "cap:k:s", "bad", "")
	require.NoError(t, err)
	require.False(t, resp.Success)
	require.Equal(t, []string{"Invalid token"}, resp.ErrorCodes)
}

func TestTurnstileVerifier_CapSiteverify_HTTPErrorWithoutBodyCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()

	v := newCapTestVerifier(t, srv.URL)
	resp, err := v.VerifyToken(context.Background(), "cap:k:s", "tok", "")
	require.NoError(t, err)
	require.False(t, resp.Success, "non-2xx must not be treated as success")
	require.Equal(t, []string{"cap-http-500"}, resp.ErrorCodes)
}

func TestTurnstileVerifier_CapSecretMalformed(t *testing.T) {
	v := newCapTestVerifier(t, "http://127.0.0.1:1")
	_, err := v.VerifyToken(context.Background(), "cap:onlykey", "tok", "")
	require.Error(t, err)
}
