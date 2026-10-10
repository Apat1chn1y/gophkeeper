// Package api exposes the GophKeeper service over HTTP/JSON.
//
// All endpoints live under /api/v1. Authentication is carried by a
// "Authorization: Bearer <jwt>" header. Payload encryption is handled
// inside the service layer, so this package only performs transport
// concerns: JSON decoding, status-code mapping, and authentication.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"gophkeeper/internal/protocol"
	"gophkeeper/internal/server/auth"
	"gophkeeper/internal/server/service"
	"gophkeeper/internal/server/storage"
)

// maxBodyBytes caps the size of any incoming request body.
const maxBodyBytes = 1 << 20 // 1 MiB

type ctxKey int

const userIDKey ctxKey = iota

// Server wires HTTP routes to a service.Service.
type Server struct {
	svc    *service.Service
	secret []byte
	log    *slog.Logger
}

// New returns a Server. If log is nil, slog.Default() is used.
func New(svc *service.Service, secret []byte, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{svc: svc, secret: secret, log: log}
}

// Routes returns the HTTP handler for the whole API, ready to be handed
// to http.Server.
//
// Uses Go 1.22 method+pattern routing, so no third-party router is needed.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("POST /api/v1/auth/register", s.handleRegister)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)

	mux.Handle("POST /api/v1/sync/push", s.auth(s.handlePush))
	mux.Handle("POST /api/v1/sync/pull", s.auth(s.handlePull))

	mux.Handle("GET /api/v1/items", s.auth(s.handleList))
	mux.Handle("GET /api/v1/items/{id}", s.auth(s.handleGet))
	mux.Handle("DELETE /api/v1/items/{id}", s.auth(s.handleDelete))

	return mux
}

// auth is middleware that validates the bearer token and stores the
// caller's user ID in the request context.
func (s *Server) auth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, prefix) {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		uid, err := auth.Verify(strings.TrimPrefix(h, prefix), s.secret)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userIDKey, uid)))
	})
}

func userID(r *http.Request) string {
	id, _ := r.Context().Value(userIDKey).(string)
	return id
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req protocol.RegisterRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	id, err := s.svc.Register(r.Context(), req.Username, req.Email, req.Password)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, protocol.RegisterResponse{UserID: id})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req protocol.LoginRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	tok, uid, err := s.svc.Login(r.Context(), req.Username, req.Password)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, protocol.LoginResponse{Token: tok, UserID: uid})
}

func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	var req protocol.PushRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	out, err := s.svc.Push(r.Context(), userID(r), req.Items)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, protocol.PushResponse{Items: out})
}

func (s *Server) handlePull(w http.ResponseWriter, r *http.Request) {
	var req protocol.PullRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	items, now, err := s.svc.Pull(r.Context(), userID(r), req.Since)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, protocol.PullResponse{Items: items, Now: now})
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	var (
		items []protocol.ItemDTO
		err   error
	)
	if q != "" {
		items, err = s.svc.SearchItems(r.Context(), userID(r), q)
	} else {
		items, err = s.svc.ListItems(r.Context(), userID(r))
	}
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, protocol.PushResponse{Items: items})
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	it, err := s.svc.GetItem(r.Context(), userID(r), id)
	if err != nil {
		s.writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, it)
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.svc.Delete(r.Context(), userID(r), id); err != nil {
		s.writeServiceError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeServiceError maps a service/storage error to an HTTP status.
// Internal errors are logged at Error level and reported to the client
// as a generic 500 — implementation details are never leaked.
func (s *Server) writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidInput):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, service.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "invalid credentials")
	case errors.Is(err, storage.ErrDuplicate):
		writeError(w, http.StatusConflict, "user already exists")
	case errors.Is(err, storage.ErrNotFound):
		writeError(w, http.StatusNotFound, "not found")
	default:
		s.log.Error("internal error", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Response is already partially written; nothing useful to do.
		return
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, protocol.ErrorResponse{Error: msg})
}
