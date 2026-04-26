package cli

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestRequireAuth_RejectsMissingToken(t *testing.T) {
	c := &Controller{authToken: "secret-token"}
	handler := c.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if got := rec.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
		t.Fatalf("expected WWW-Authenticate: Bearer, got %q", got)
	}
}

func TestRequireAuth_AcceptsBearerHeader(t *testing.T) {
	c := &Controller{authToken: "secret-token"}
	called := false
	handler := c.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if !called {
		t.Fatal("handler should have been invoked with valid bearer token")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func TestRequireAuth_AcceptsCookie(t *testing.T) {
	c := &Controller{authToken: "secret-token"}
	called := false
	handler := c.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	req := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	req.AddCookie(&http.Cookie{Name: authCookieName, Value: "secret-token"})
	rec := httptest.NewRecorder()
	handler(rec, req)

	if !called {
		t.Fatal("handler should have been invoked with valid cookie")
	}
}

func TestRequireAuth_RejectsWrongToken(t *testing.T) {
	c := &Controller{authToken: "secret-token"}
	handler := c.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called with wrong token")
	})

	req := httptest.NewRequest(http.MethodGet, "/api/nodes", nil)
	req.Header.Set("Authorization", "Bearer wrong")
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestRequireAuth_RedirectsBrowserRequests(t *testing.T) {
	c := &Controller{authToken: "secret-token"}
	handler := c.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 redirect for unauthenticated /, got %d", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/login" {
		t.Fatalf("expected redirect to /login, got %q", got)
	}
}

func TestHandleLogin_SetsCookieOnValidToken(t *testing.T) {
	c := &Controller{authToken: "secret-token"}

	form := url.Values{"token": {"secret-token"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	c.handleLogin(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 after successful login, got %d", rec.Code)
	}
	var found bool
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == authCookieName && ck.Value == "secret-token" {
			found = true
			if !ck.HttpOnly {
				t.Error("auth cookie must be HttpOnly")
			}
			if ck.SameSite != http.SameSiteStrictMode {
				t.Error("auth cookie must be SameSite=Strict")
			}
		}
	}
	if !found {
		t.Fatal("expected auth cookie to be set")
	}
}

func TestHandleLogin_RendersTemplate(t *testing.T) {
	c := &Controller{authToken: "secret-token"}

	req := httptest.NewRequest(http.MethodGet, "/login", nil)
	rec := httptest.NewRecorder()
	c.handleLogin(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`<form method="POST" action="/login"`, `STRESSTOOL_WEB_TOKEN`, `/login.css`} {
		if !strings.Contains(body, want) {
			t.Errorf("login page missing %q\nbody: %s", want, body)
		}
	}
}

func TestHandleLogin_RejectsWrongToken(t *testing.T) {
	c := &Controller{authToken: "secret-token"}

	form := url.Values{"token": {"wrong"}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	c.handleLogin(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	for _, ck := range rec.Result().Cookies() {
		if ck.Name == authCookieName && ck.Value != "" && ck.MaxAge >= 0 {
			t.Fatal("auth cookie must not be set on failed login")
		}
	}
}
