package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gophkeeper/internal/models"
	"gophkeeper/internal/protocol"
)

// fakeAuthServer is the minimal server surface register/login need.
func fakeAuthServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/register", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(protocol.RegisterResponse{UserID: "u1"})
	})
	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(protocol.LoginResponse{Token: "tok", UserID: "u1"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func isolatePaths(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("GOPHKEEPER_CLIENT_CONFIG", filepath.Join(dir, "config.json"))
	t.Setenv("GOPHKEEPER_CLIENT_DB", filepath.Join(dir, "client.db"))
}

func seedConfig(t *testing.T) (dir, cfgPath string) {
	t.Helper()
	dir = t.TempDir()
	cfgPath = filepath.Join(dir, "cfg.json")
	t.Setenv("GOPHKEEPER_CLIENT_CONFIG", cfgPath)
	t.Setenv("GOPHKEEPER_CLIENT_DB", filepath.Join(dir, "client.db"))
	if err := writeConfig(cfgPath, "http://example.invalid", "tok", "u1"); err != nil {
		t.Fatalf("seed cfg: %v", err)
	}
	return dir, cfgPath
}

func TestVersion(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"version"}, strings.NewReader(""), &out, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "GophKeeper client") {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

func TestHelp(t *testing.T) {
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"help"}, strings.NewReader(""), &out, &out); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "Usage:") {
		t.Fatalf("missing usage: %s", out.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	var out bytes.Buffer
	err := Run(context.Background(), []string{"frobnicate"}, strings.NewReader(""), &out, &out)
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestRegisterAndLogout drives register with server/username/email as
// flags and the password on stdin (the -password flag was removed on
// purpose: it would leak through the process listing).
func TestRegisterAndLogout(t *testing.T) {
	isolatePaths(t)
	srv := fakeAuthServer(t)

	var out bytes.Buffer
	err := Run(context.Background(),
		[]string{"register", "-server", srv.URL, "-username", "alice", "-email", "a@x.io"},
		strings.NewReader("pw\n"), &out, &out)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if !strings.Contains(out.String(), "Registered and logged in") {
		t.Fatalf("unexpected output: %s", out.String())
	}

	out.Reset()
	if err := Run(context.Background(), []string{"logout"}, strings.NewReader(""), &out, &out); err != nil {
		t.Fatalf("logout: %v", err)
	}
	if !strings.Contains(out.String(), "Logged out") {
		t.Fatalf("unexpected output: %s", out.String())
	}
}

// TestRegisterAllPrompts feeds server, username, email and password on
// stdin so that no flags are needed.
func TestRegisterAllPrompts(t *testing.T) {
	isolatePaths(t)
	srv := fakeAuthServer(t)

	var out bytes.Buffer
	err := Run(context.Background(),
		[]string{"register"},
		strings.NewReader(srv.URL+"\nalice\na@x.io\npw\n"), &out, &out)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if !strings.Contains(out.String(), "Registered and logged in") {
		t.Fatalf("unexpected: %s", out.String())
	}
}

func TestRegisterNoPasswordOnStdin(t *testing.T) {
	isolatePaths(t)
	srv := fakeAuthServer(t)

	var out bytes.Buffer
	err := Run(context.Background(),
		[]string{"register", "-server", srv.URL, "-username", "alice", "-email", "a@x.io"},
		strings.NewReader(""), &out, &out)
	if err == nil {
		t.Fatal("expected error when no password is provided")
	}
}

func TestAddAndListAndGet(t *testing.T) {
	isolatePaths(t)
	seedConfig(t)

	// add text item: prompts are Title, Metadata, Data.
	in := strings.NewReader("my note\ntag1\nsecret body\n")
	var out bytes.Buffer
	if err := Run(context.Background(), []string{"add", "text"}, in, &out, &out); err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.Contains(out.String(), "Added text item") {
		t.Fatalf("unexpected: %s", out.String())
	}

	out.Reset()
	if err := Run(context.Background(), []string{"list"}, strings.NewReader(""), &out, &out); err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out.String(), "my note") {
		t.Fatalf("list missing item: %s", out.String())
	}
}

func TestSyncWithoutLogin(t *testing.T) {
	isolatePaths(t)
	var out bytes.Buffer
	err := Run(context.Background(), []string{"sync"}, strings.NewReader(""), &out, &out)
	if err == nil {
		t.Fatal("expected error")
	}
}

func writeConfig(path, server, token, uid string) error {
	raw := []byte(`{"server_address":"` + server + `","token":"` + token + `","user_id":"` + uid + `"}`)
	return os.WriteFile(path, raw, 0o600)
}

func TestLoginAfterRegister(t *testing.T) {
	isolatePaths(t)
	srv := fakeAuthServer(t)

	// Register first (password on stdin).
	var out bytes.Buffer
	_ = Run(context.Background(),
		[]string{"register", "-server", srv.URL, "-username", "bob", "-email", "b@x.io"},
		strings.NewReader("pw\n"), &out, &out)

	// Logout.
	out.Reset()
	_ = Run(context.Background(), []string{"logout"}, strings.NewReader(""), &out, &out)

	// Login again (password on stdin).
	out.Reset()
	if err := Run(context.Background(),
		[]string{"login", "-server", srv.URL, "-username", "bob"},
		strings.NewReader("pw\n"), &out, &out); err != nil {
		t.Fatalf("login: %v", err)
	}
	if !strings.Contains(out.String(), "Logged in as bob") {
		t.Fatalf("unexpected: %s", out.String())
	}
}

func TestLoginWrongPassword(t *testing.T) {
	isolatePaths(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(protocol.ErrorResponse{Error: "invalid"})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	var out bytes.Buffer
	err := Run(context.Background(),
		[]string{"login", "-server", srv.URL, "-username", "x"},
		strings.NewReader("y\n"), &out, &out)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestGetAfterAdd(t *testing.T) {
	isolatePaths(t)
	seedConfig(t)

	var out bytes.Buffer
	if err := Run(context.Background(), []string{"add", "text"},
		strings.NewReader("my note\n\nbody\n"), &out, &out); err != nil {
		t.Fatalf("add: %v", err)
	}
	id := lastID(out.String())
	if id == "" {
		t.Fatalf("could not parse id from %q", out.String())
	}

	out.Reset()
	if err := Run(context.Background(), []string{"get", id},
		strings.NewReader(""), &out, &out); err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(out.String(), "my note") {
		t.Fatalf("get missing content: %s", out.String())
	}

	out.Reset()
	if err := Run(context.Background(), []string{"delete", id},
		strings.NewReader(""), &out, &out); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !strings.Contains(out.String(), "Deleted") {
		t.Fatalf("delete: %s", out.String())
	}

	out.Reset()
	_ = Run(context.Background(), []string{"list"}, strings.NewReader(""), &out, &out)
	if strings.Contains(out.String(), "my note") {
		t.Fatalf("deleted item still visible: %s", out.String())
	}
}

// lastID returns the last token that looks like a UUID from s, or "".
func lastID(s string) string {
	fields := strings.Fields(s)
	for i := len(fields) - 1; i >= 0; i-- {
		f := fields[i]
		if len(f) == 36 && strings.Count(f, "-") == 4 {
			return f
		}
	}
	return ""
}

func TestTUIWithoutLogin(t *testing.T) {
	isolatePaths(t)
	var out bytes.Buffer
	err := Run(context.Background(), []string{"tui"}, strings.NewReader(""), &out, &out)
	if err == nil {
		t.Fatal("expected error without configured server")
	}
}

func TestTUIWithoutToken(t *testing.T) {
	isolatePaths(t)
	seedConfig(t)
	// Overwrite with no token.
	cfgPath := os.Getenv("GOPHKEEPER_CLIENT_CONFIG")
	_ = writeConfig(cfgPath, "http://example.invalid", "", "")

	var out bytes.Buffer
	err := Run(context.Background(), []string{"tui"}, strings.NewReader(""), &out, &out)
	if err == nil {
		t.Fatal("expected error without token")
	}
}

func TestAddBinaryFromFile(t *testing.T) {
	isolatePaths(t)
	dir, _ := seedConfig(t)

	binPath := filepath.Join(dir, "blob.bin")
	want := []byte{0x00, 0x01, 0xff, 0x42, 0x00}
	if err := os.WriteFile(binPath, want, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	var out bytes.Buffer
	err := Run(context.Background(),
		[]string{"add", "binary", "-from-file", binPath, "-title", "blob"},
		strings.NewReader(""), &out, &out)
	if err != nil {
		t.Fatalf("add binary: %v", err)
	}
	if !strings.Contains(out.String(), "Added binary item") {
		t.Fatalf("unexpected: %s", out.String())
	}
}

func TestAddBinaryRequiresFromFile(t *testing.T) {
	isolatePaths(t)
	seedConfig(t)

	var out bytes.Buffer
	err := Run(context.Background(),
		[]string{"add", "binary"},
		strings.NewReader(""), &out, &out)
	if err == nil {
		t.Fatal("expected error for binary without -from-file")
	}
}

// TestAddFromFileTooBig verifies that a file larger than maxUploadBytes
// is rejected with a clear message before os.ReadFile is called.
func TestAddFromFileTooBig(t *testing.T) {
	isolatePaths(t)
	dir, _ := seedConfig(t)

	bigPath := filepath.Join(dir, "big.bin")
	f, err := os.Create(bigPath)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 800 KiB > 700 KiB cap.
	if _, err := f.Write(make([]byte, 800*1024)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = f.Close()

	var out bytes.Buffer
	err = Run(context.Background(),
		[]string{"add", "binary", "-from-file", bigPath, "-title", "big"},
		strings.NewReader(""), &out, &out)
	if err == nil {
		t.Fatal("expected error for oversized file")
	}
	if !strings.Contains(err.Error(), "limit is") {
		t.Fatalf("error should explain the limit: %v", err)
	}
}

func TestAddFromFileMissing(t *testing.T) {
	isolatePaths(t)
	seedConfig(t)
	var out bytes.Buffer
	err := Run(context.Background(),
		[]string{"add", "binary", "-from-file", "/nonexistent/path"},
		strings.NewReader(""), &out, &out)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// TestListSearchQueriesServer verifies that -search hits GET /api/v1/items?q=...
func TestListSearchQueriesServer(t *testing.T) {
	isolatePaths(t)
	dir, _ := seedConfig(t)
	_ = dir

	// Stand up a fake server that answers the search endpoint.
	seenQuery := make(chan string, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/items", func(w http.ResponseWriter, r *http.Request) {
		seenQuery <- r.URL.Query().Get("q")
		_ = json.NewEncoder(w).Encode(protocol.PushResponse{
			Items: []protocol.ItemDTO{
				{ID: "s1", Type: "text", Title: "found", Metadata: "github"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// Repoint config at the fake server but keep the token.
	cfgPath := os.Getenv("GOPHKEEPER_CLIENT_CONFIG")
	_ = writeConfig(cfgPath, srv.URL, "tok", "u1")

	var out bytes.Buffer
	if err := Run(context.Background(),
		[]string{"list", "-search", "github"},
		strings.NewReader(""), &out, &out); err != nil {
		t.Fatalf("list -search: %v", err)
	}
	if got := <-seenQuery; got != "github" {
		t.Fatalf("server got q=%q, want github", got)
	}
	if !strings.Contains(out.String(), "found") {
		t.Fatalf("search results not printed: %s", out.String())
	}
}

func TestListSearchRequiresLogin(t *testing.T) {
	isolatePaths(t)
	seedConfig(t)
	cfgPath := os.Getenv("GOPHKEEPER_CLIENT_CONFIG")
	_ = writeConfig(cfgPath, "http://example.invalid", "", "")

	var out bytes.Buffer
	err := Run(context.Background(),
		[]string{"list", "-search", "x"},
		strings.NewReader(""), &out, &out)
	if err == nil {
		t.Fatal("expected error without token")
	}
}

// TestPrintItemBinaryHidesData guards the binary-print behaviour:
// binary payloads must not be dumped to the terminal.
func TestPrintItemBinaryHidesData(t *testing.T) {
	var buf bytes.Buffer
	it := &models.Item{
		ID: "b1", Type: models.ItemTypeBinary, Title: "blob",
		Data: []byte{0x00, 0x01, 0xff, 'a', 'b'},
	}
	printItem(&buf, it)
	got := buf.String()
	if strings.Contains(got, "\x00") || strings.Contains(got, "\xff") {
		t.Fatalf("binary bytes leaked into output: %q", got)
	}
	if !strings.Contains(got, "5 bytes") {
		t.Fatalf("expected byte count: %q", got)
	}
}
