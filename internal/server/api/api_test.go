package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gophkeeper/internal/crypto"
	"gophkeeper/internal/models"
	"gophkeeper/internal/protocol"
	"gophkeeper/internal/server/service"
	"gophkeeper/internal/server/storage/sqlite"
)

var testSecret = []byte("0123456789abcdef0123456789abcdef")

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	store, err := sqlite.Open(":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	key, _ := crypto.GenerateKey()
	enc, _ := crypto.NewEncryptor(key)
	svc := service.New(store, enc, testSecret, time.Hour)
	srv := httptest.NewServer(New(svc, testSecret, nil).Routes())
	t.Cleanup(srv.Close)
	return srv
}

func doJSON(t *testing.T, method, url string, token string, body any) (*http.Response, []byte) {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	req, _ := http.NewRequestWithContext(context.Background(), method, url, &buf)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	var out bytes.Buffer
	_, _ = out.ReadFrom(resp.Body)
	return resp, out.Bytes()
}

func registerAndLogin(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	resp, body := doJSON(t, http.MethodPost, srv.URL+"/api/v1/auth/register", "", protocol.RegisterRequest{
		Username: "alice", Email: "a@x.io", Password: "hunter2",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status %d: %s", resp.StatusCode, body)
	}
	resp, body = doJSON(t, http.MethodPost, srv.URL+"/api/v1/auth/login", "", protocol.LoginRequest{
		Username: "alice", Password: "hunter2",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status %d: %s", resp.StatusCode, body)
	}
	var lr protocol.LoginResponse
	if err := json.Unmarshal(body, &lr); err != nil {
		t.Fatalf("unmarshal login: %v", err)
	}
	return lr.Token
}

func TestRegisterLoginFlow(t *testing.T) {
	srv := newTestServer(t)
	tok := registerAndLogin(t, srv)
	if tok == "" {
		t.Fatal("empty token")
	}
}

func TestRegisterDuplicateConflict(t *testing.T) {
	srv := newTestServer(t)
	_ = registerAndLogin(t, srv)
	resp, _ := doJSON(t, http.MethodPost, srv.URL+"/api/v1/auth/register", "", protocol.RegisterRequest{
		Username: "alice", Email: "a@x.io", Password: "x",
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("want 409, got %d", resp.StatusCode)
	}
}

func TestLoginBadCredentials(t *testing.T) {
	srv := newTestServer(t)
	_ = registerAndLogin(t, srv)
	resp, _ := doJSON(t, http.MethodPost, srv.URL+"/api/v1/auth/login", "", protocol.LoginRequest{
		Username: "alice", Password: "wrong",
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
}

func TestAuthMiddlewareRejectsMissingToken(t *testing.T) {
	srv := newTestServer(t)
	resp, _ := doJSON(t, http.MethodGet, srv.URL+"/api/v1/items", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
}

func TestAuthMiddlewareRejectsBadToken(t *testing.T) {
	srv := newTestServer(t)
	resp, _ := doJSON(t, http.MethodGet, srv.URL+"/api/v1/items", "garbage", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
}

func TestPushPullListGetDelete(t *testing.T) {
	srv := newTestServer(t)
	tok := registerAndLogin(t, srv)
	now := time.Now().UTC()

	// Push two items.
	resp, body := doJSON(t, http.MethodPost, srv.URL+"/api/v1/sync/push", tok, protocol.PushRequest{
		Items: []protocol.ItemDTO{
			{ID: "i1", Type: string(models.ItemTypeText), Title: "a", Data: []byte("x"), UpdatedAt: now},
			{ID: "i2", Type: string(models.ItemTypeCard), Title: "b", Data: []byte("y"), UpdatedAt: now},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("push status %d: %s", resp.StatusCode, body)
	}
	var pr protocol.PushResponse
	_ = json.Unmarshal(body, &pr)
	if len(pr.Items) != 2 || pr.Items[0].Version != 1 {
		t.Fatalf("unexpected push response: %+v", pr)
	}

	// List.
	resp, body = doJSON(t, http.MethodGet, srv.URL+"/api/v1/items", tok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list status %d", resp.StatusCode)
	}
	var lr protocol.PushResponse
	_ = json.Unmarshal(body, &lr)
	if len(lr.Items) != 2 {
		t.Fatalf("want 2 items, got %d", len(lr.Items))
	}

	// Get one.
	resp, body = doJSON(t, http.MethodGet, srv.URL+"/api/v1/items/i1", tok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status %d: %s", resp.StatusCode, body)
	}
	var single protocol.ItemDTO
	_ = json.Unmarshal(body, &single)
	if single.Title != "a" || string(single.Data) != "x" {
		t.Fatalf("unexpected item: %+v", single)
	}

	// Pull since before anything.
	resp, body = doJSON(t, http.MethodPost, srv.URL+"/api/v1/sync/pull", tok, protocol.PullRequest{Since: time.Time{}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pull status %d", resp.StatusCode)
	}
	var pp protocol.PullResponse
	_ = json.Unmarshal(body, &pp)
	if len(pp.Items) != 2 || pp.Now.IsZero() {
		t.Fatalf("unexpected pull: %+v", pp)
	}

	// Delete i1.
	resp, _ = doJSON(t, http.MethodDelete, srv.URL+"/api/v1/items/i1", tok, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status %d", resp.StatusCode)
	}
	resp, body = doJSON(t, http.MethodGet, srv.URL+"/api/v1/items", tok, nil)
	_ = json.Unmarshal(body, &lr)
	if len(lr.Items) != 1 || lr.Items[0].ID != "i2" {
		t.Fatalf("after delete: %+v", lr.Items)
	}
}

func TestGetMissingItemReturns404(t *testing.T) {
	srv := newTestServer(t)
	tok := registerAndLogin(t, srv)
	resp, _ := doJSON(t, http.MethodGet, srv.URL+"/api/v1/items/nope", tok, nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
}

func TestPushValidationError(t *testing.T) {
	srv := newTestServer(t)
	tok := registerAndLogin(t, srv)
	resp, _ := doJSON(t, http.MethodPost, srv.URL+"/api/v1/sync/push", tok, protocol.PushRequest{
		Items: []protocol.ItemDTO{
			{ID: "bad", Type: "nope", Title: "x", UpdatedAt: time.Now()},
		},
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestBadJSONReturns400(t *testing.T) {
	srv := newTestServer(t)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/auth/register", bytes.NewBufferString("not-json"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestSearchEndpointFiltersByQuery(t *testing.T) {
	srv := newTestServer(t)
	tok := registerAndLogin(t, srv)
	now := time.Now().UTC()

	_, _ = doJSON(t, http.MethodPost, srv.URL+"/api/v1/sync/push", tok, protocol.PushRequest{
		Items: []protocol.ItemDTO{
			{ID: "i1", Type: "text", Title: "gh",
				Metadata: "github", Data: []byte("x"), UpdatedAt: now},
			{ID: "i2", Type: "text", Title: "other",
				Metadata: "example", Data: []byte("y"), UpdatedAt: now},
		},
	})

	resp, body := doJSON(t, http.MethodGet, srv.URL+"/api/v1/items?q=github", tok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("search status %d: %s", resp.StatusCode, body)
	}
	var got protocol.PushResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].ID != "i1" {
		t.Fatalf("unexpected: %+v", got.Items)
	}
}

func TestSearchEndpointRequiresAuth(t *testing.T) {
	srv := newTestServer(t)
	resp, _ := doJSON(t, http.MethodGet, srv.URL+"/api/v1/items?q=x", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
}

func TestSearchEmptyQueryReturnsAll(t *testing.T) {
	srv := newTestServer(t)
	tok := registerAndLogin(t, srv)
	now := time.Now().UTC()
	_, _ = doJSON(t, http.MethodPost, srv.URL+"/api/v1/sync/push", tok, protocol.PushRequest{
		Items: []protocol.ItemDTO{
			{ID: "i1", Type: "text", Title: "a", UpdatedAt: now},
			{ID: "i2", Type: "text", Title: "b", UpdatedAt: now},
		},
	})

	resp, body := doJSON(t, http.MethodGet, srv.URL+"/api/v1/items?q=", tok, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d: %s", resp.StatusCode, body)
	}
	var got protocol.PushResponse
	_ = json.Unmarshal(body, &got)
	if len(got.Items) != 2 {
		t.Fatalf("empty q should return all items, got %+v", got.Items)
	}
}
