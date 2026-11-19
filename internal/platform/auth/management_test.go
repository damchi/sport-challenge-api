package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	testClientID     = "mgmt-client"
	testClientSecret = "mgmt-s3cr3t"
)

// fakeAuth0 answers the token and user deletion endpoints of the Management
// API. tokenStatus and deleteStatus set the answers; deleted records the
// escaped path of the deleted user.
type fakeAuth0 struct {
	tokenStatus  int
	deleteStatus int
	deleted      string
	auth         string
}

func (f *fakeAuth0) serve(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/oauth/token":
			if err := r.ParseForm(); err != nil {
				t.Errorf("token form: %v", err)
			}
			want := map[string]string{
				"grant_type": "client_credentials", "client_id": testClientID,
				"client_secret": testClientSecret, "audience": "http://" + r.Host + "/api/v2/",
			}
			for k, v := range want {
				if got := r.PostForm.Get(k); got != v {
					t.Errorf("token form %s = %q, want %q", k, got, v)
				}
			}
			w.WriteHeader(f.tokenStatus)
			_, _ = w.Write([]byte(`{"access_token":"mgmt-token","token_type":"Bearer"}`))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.EscapedPath(), "/api/v2/users/"):
			f.deleted = strings.TrimPrefix(r.URL.EscapedPath(), "/api/v2/users/")
			f.auth = r.Header.Get("Authorization")
			w.WriteHeader(f.deleteStatus)
		default:
			t.Errorf("unexpected call %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusTeapot)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestNewAuth0ManagementUsesTheTenantDomain: the address, and so the
// audience (base URL + /api/v2/, checked by fakeAuth0), come from the domain
// given, the tenant's (config AUTH0_MGMT_DOMAIN), never the custom domain
// that issues the tokens.
func TestNewAuth0ManagementUsesTheTenantDomain(t *testing.T) {
	m := NewAuth0Management("tenant.ca.auth0.com", testClientID, testClientSecret)
	if m.baseURL != "https://tenant.ca.auth0.com" {
		t.Fatalf("baseURL = %q, want https://tenant.ca.auth0.com", m.baseURL)
	}
}

func TestDeleteUser(t *testing.T) {
	tests := []struct {
		name         string
		tokenStatus  int
		deleteStatus int
		wantErr      bool
	}{
		{name: "deleted", tokenStatus: http.StatusOK, deleteStatus: http.StatusNoContent},
		{name: "unknown at Auth0: already deleted", tokenStatus: http.StatusOK, deleteStatus: http.StatusNotFound},
		{name: "deletion refused", tokenStatus: http.StatusOK, deleteStatus: http.StatusForbidden, wantErr: true},
		{name: "deletion failure", tokenStatus: http.StatusOK, deleteStatus: http.StatusServiceUnavailable, wantErr: true},
		{name: "token refused", tokenStatus: http.StatusUnauthorized, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeAuth0{tokenStatus: tt.tokenStatus, deleteStatus: tt.deleteStatus}
			srv := fake.serve(t)
			err := newManagement(srv.URL, testClientID, testClientSecret).DeleteUser(t.Context(), "email|65f0c2a1")
			if (err != nil) != tt.wantErr {
				t.Fatalf("DeleteUser() error = %v, want error %v", err, tt.wantErr)
			}
			if err != nil && strings.Contains(err.Error(), testClientSecret) {
				t.Fatalf("error leaks the client secret: %v", err)
			}
			if tt.tokenStatus != http.StatusOK {
				if fake.deleted != "" {
					t.Fatal("user deleted without a token")
				}
				return
			}
			// The subject is escaped in the path: "|" would otherwise be sent raw.
			if fake.deleted != "email%7C65f0c2a1" || fake.auth != "Bearer mgmt-token" {
				t.Fatalf("deleted %q with %q, want the escaped subject and the token", fake.deleted, fake.auth)
			}
		})
	}
}

func TestDeleteUserUnreachable(t *testing.T) {
	// The token works, then the deletion's connection breaks: the error must
	// not carry the URL, which holds the subject.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oauth/token" {
			_, _ = w.Write([]byte(`{"access_token":"mgmt-token"}`))
			return
		}
		conn, _, err := http.NewResponseController(w).Hijack()
		if err == nil {
			_ = conn.Close()
		}
	}))
	t.Cleanup(srv.Close)
	err := newManagement(srv.URL, testClientID, testClientSecret).DeleteUser(t.Context(), "email|65f0c2a1")
	if err == nil || strings.Contains(err.Error(), "65f0c2a1") || strings.Contains(err.Error(), testClientSecret) {
		t.Fatalf("DeleteUser() error = %v, want an error without the subject or the secret", err)
	}
}

func TestDeleteUserNeverFollowsARedirect(t *testing.T) {
	var elsewhere bool
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { elsewhere = true }))
	t.Cleanup(other.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A 307 would resend the form, client secret included.
		http.Redirect(w, r, other.URL+"/oauth/token", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(srv.Close)
	if err := newManagement(srv.URL, testClientID, testClientSecret).DeleteUser(t.Context(), "email|1"); err == nil {
		t.Fatal("DeleteUser() error = nil, want the redirect refused")
	}
	if elsewhere {
		t.Fatal("the redirect was followed: the secret left for another host")
	}
}

func TestDeleteUserHonoursTheContext(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-block }))
	t.Cleanup(func() { close(block); srv.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := newManagement(srv.URL, testClientID, testClientSecret).DeleteUser(ctx, "email|1"); err == nil {
		t.Fatal("DeleteUser() error = nil, want the deadline")
	}
}
