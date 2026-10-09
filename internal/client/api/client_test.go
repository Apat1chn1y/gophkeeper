package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gophkeeper/internal/protocol"
)

func newServer(t *testing.T, mux *http.ServeMux) (*httptest.Server, *Client) {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, New(srv.URL, srv.Client())
}

func TestRegisterSuccess(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/register", func(w http.ResponseWriter, r *http.Request) {
		var req protocol.RegisterRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		if req.Username != "alice" {
			t.Errorf("unexpected username: %s", req.Username)
		}
		_ = json.NewEncoder(w).Encode(protocol.RegisterResponse{UserID: "u1"})
	})
	_, c := newServer(t, mux)

	id, err := c.Register(context.Background(), "alice", "a@x.io", "pw")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if id != "u1" {
		t.Fatalf("id = %q", id)
	}
}

func TestHTTPErrorPropagatesMessage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(protocol.ErrorResponse{Error: "bad creds"})
	})
	_, c := newServer(t, mux)

	_, _, err := c.Login(context.Background(), "a", "b")
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsStatus(err, http.StatusUnauthorized) {
		t.Fatalf("want 401, got %v", err)
	}
	he, ok := err.(*HTTPError)
	if !ok || he.Message != "bad creds" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPushSendsBearer(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/sync/push", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("auth header = %q", got)
		}
		_ = json.NewEncoder(w).Encode(protocol.PushResponse{
			Items: []protocol.ItemDTO{{ID: "i1", Version: 2}},
		})
	})
	_, c := newServer(t, mux)

	out, err := c.Push(context.Background(), "secret", []protocol.ItemDTO{{ID: "i1"}})
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if len(out) != 1 || out[0].Version != 2 {
		t.Fatalf("unexpected: %+v", out)
	}
}

func TestGetAndDelete(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(protocol.ItemDTO{ID: r.PathValue("id"), Title: "hit"})
	})
	mux.HandleFunc("DELETE /api/v1/items/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	_, c := newServer(t, mux)

	got, err := c.Get(context.Background(), "t", "abc")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Title != "hit" {
		t.Fatalf("got %+v", got)
	}
	if err := c.Delete(context.Background(), "t", "abc"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
}

func TestNetworkErrorIsWrapped(t *testing.T) {
	c := New("http://127.0.0.1:1", nil)
	if _, err := c.List(context.Background(), "t"); err == nil {
		t.Fatal("expected network error")
	}
}
