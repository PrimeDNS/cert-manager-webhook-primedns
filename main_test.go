package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	extapi "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/cert-manager/cert-manager/pkg/acme/webhook/apis/acme/v1alpha1"
)

func solverWithKey(key string) *primeDNSSolver {
	return &primeDNSSolver{getSecret: func(_ context.Context, ns, name string) (*corev1.Secret, error) {
		return &corev1.Secret{Data: map[string][]byte{"api-key": []byte(key + "\n")}}, nil
	}}
}

func challenge(apiURL string) *v1alpha1.ChallengeRequest {
	cfg, _ := json.Marshal(map[string]any{
		"apiKeySecretRef": map[string]string{"name": "primedns", "key": "api-key"},
		"apiUrl":          apiURL,
	})
	return &v1alpha1.ChallengeRequest{
		ResolvedFQDN:      "_ACME-challenge.Example.com.",
		Key:               "token-value",
		ResourceNamespace: "default",
		Config:            &extapi.JSON{Raw: cfg},
	}
}

func TestPresentAndCleanUpSendNameAndValue(t *testing.T) {
	var got []map[string]string
	var methods []string
	var keys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]string
		require.NoError(t, json.Unmarshal(b, &m))
		got = append(got, m)
		methods = append(methods, r.Method+" "+r.URL.Path)
		keys = append(keys, r.Header.Get("X-API-Key"))
		w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()

	s := solverWithKey("pdns_secret")
	require.NoError(t, s.Present(challenge(srv.URL)))
	require.NoError(t, s.CleanUp(challenge(srv.URL)))

	assert.Equal(t, []string{"POST /dns/acme-challenge", "DELETE /dns/acme-challenge"}, methods)
	for _, m := range got {
		assert.Equal(t, map[string]string{"name": "_acme-challenge.example.com", "value": "token-value"}, m)
	}
	assert.Equal(t, []string{"pdns_secret", "pdns_secret"}, keys)
}

func TestScopeErrorIsExplained(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"success":false,"error":{"code":"NAME_NOT_ALLOWED","message":"not allowed"}}`))
	}))
	defer srv.Close()
	err := solverWithKey("k").Present(challenge(srv.URL))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "HTTP 403 NAME_NOT_ALLOWED")
	assert.Contains(t, err.Error(), "acme:name:")
}

func TestRetriesOnceOnRateLimit(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&n, 1) == 1 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(429)
			return
		}
		w.Write([]byte(`{"success":true}`))
	}))
	defer srv.Close()
	c := newAPIClient(srv.URL, "k")
	var slept time.Duration
	c.sleep = func(d time.Duration) { slept = d }
	require.NoError(t, c.Present(context.Background(), "_acme-challenge.example.com", "v"))
	assert.EqualValues(t, 2, n)
	assert.Equal(t, 7*time.Second, slept)
}

func TestSecondFailureIsReturned(t *testing.T) {
	var n int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&n, 1)
		w.WriteHeader(503)
	}))
	defer srv.Close()
	c := newAPIClient(srv.URL, "k")
	c.sleep = func(time.Duration) {}
	require.Error(t, c.Present(context.Background(), "_acme-challenge.example.com", "v"))
	assert.EqualValues(t, 2, n)
}

func TestRetryAfterSeconds(t *testing.T) {
	assert.Equal(t, 5*time.Second, retryAfterSeconds(""))
	assert.Equal(t, 5*time.Second, retryAfterSeconds("Wed, 21 Oct 2026 07:28:00 GMT"))
	assert.Equal(t, 9*time.Second, retryAfterSeconds(" 9 "))
	assert.Equal(t, 60*time.Second, retryAfterSeconds("600"))
}

func TestConfigErrors(t *testing.T) {
	s := solverWithKey("k")
	ch := challenge("http://x")
	ch.Config = nil
	assert.ErrorContains(t, s.Present(ch), "apiKeySecretRef")

	ch = challenge("http://x")
	empty := &primeDNSSolver{getSecret: func(context.Context, string, string) (*corev1.Secret, error) {
		return &corev1.Secret{Data: map[string][]byte{}}, nil
	}}
	assert.ErrorContains(t, empty.Present(ch), `has no key "api-key"`)

	assert.ErrorContains(t, solverWithKey("  ").Present(ch), "is empty")
	assert.ErrorContains(t, (&primeDNSSolver{}).Present(ch), "not initialized")
}

func TestCleanUpWithoutScopeIsNotAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"success":false,"error":{"code":"NAME_NOT_ALLOWED","message":"not allowed"}}`))
	}))
	defer srv.Close()
	s := solverWithKey("k")
	require.NoError(t, s.CleanUp(challenge(srv.URL)))
	assert.Error(t, s.Present(challenge(srv.URL)))
}

func TestCleanUpOtherErrorsAreReturned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
	}))
	defer srv.Close()
	assert.Error(t, solverWithKey("k").CleanUp(challenge(srv.URL)))
}
