// Package cli implements the GophKeeper command-line client.
//
// The package is deliberately decoupled from os.Stdin/os.Stdout so it can
// be driven from tests. Every command is a thin orchestrator over the
// config, localstore, api and sync packages.
package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"gophkeeper/internal/client/api"
	"gophkeeper/internal/client/config"
	"gophkeeper/internal/client/localstore"
	"gophkeeper/internal/client/sync"
	"gophkeeper/internal/client/tui"
	"gophkeeper/internal/models"
)

// Build metadata injected at link time with -ldflags.
var (
	// BuildVersion is the client version.
	BuildVersion = "dev"
	// BuildDate is the build timestamp.
	BuildDate = "unknown"
)

// Run executes the client with the given arguments. args[0] is the
// subcommand name, args[1:] its arguments.
//
// stdin is used for interactive prompts; stdout and stderr for output.
func Run(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	_ = stderr // reserved for future use
	if len(args) == 0 {
		printUsage(stdout)
		return nil
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "help", "-h", "--help":
		printUsage(stdout)
		return nil
	case "version":
		fmt.Fprintf(stdout, "GophKeeper client %s (built %s)\n", BuildVersion, BuildDate)
		return nil
	case "register":
		return runRegister(ctx, rest, stdin, stdout)
	case "login":
		return runLogin(ctx, rest, stdin, stdout)
	case "logout":
		return runLogout(ctx, stdout)
	case "sync":
		return runSync(ctx, stdout)
	case "add":
		return runAdd(ctx, rest, stdin, stdout)
	case "list":
		return runList(ctx, rest, stdout)
	case "get":
		return runGet(ctx, rest, stdout)
	case "delete":
		return runDelete(ctx, rest, stdout)
	case "tui":
		return runTUI(ctx)
	default:
		return fmt.Errorf("unknown command %q (try `client help`)", cmd)
	}
}

func runTUI(ctx context.Context) error {
	e, err := openEnv()
	if err != nil {
		return err
	}
	defer e.close()
	if err := e.requireAuth(); err != nil {
		return err
	}
	return tui.Run(ctx, e.store, sync.New(e.store, e.api, e.cfg.Token))
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `GophKeeper client

Usage:
  client <command> [flags]

Commands:
  register         Register a new account
  login            Authenticate and store a session token
  logout           Forget the local session token
  sync             Exchange local and remote changes with the server
  add <type>       Add a new item (credentials|text|binary|card).
                   Flags: -title, -metadata, -from-file <path>
  list [type]      List stored items, optionally filtered by type
  get <id>         Show a single item by ID
  delete <id>      Soft-delete an item
  version          Print version information
  help             Show this message
  tui              Start the interactive terminal UI
`)
}

// env bundles the state every authenticated command needs.
type env struct {
	cfg   *config.Config
	store *localstore.Store
	api   *api.Client
}

// openEnv loads the config, opens the local store and returns an env.
// It does not require the user to be logged in.
func openEnv() (*env, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if cfg.ServerAddress == "" {
		return nil, errors.New("client: server address not configured; run `client register` or `client login` first")
	}
	store, err := openStore()
	if err != nil {
		return nil, err
	}
	return &env{cfg: cfg, store: store, api: api.New(cfg.ServerAddress, nil)}, nil
}

// close releases the local store. Safe to call on a nil env.
func (e *env) close() {
	if e != nil && e.store != nil {
		_ = e.store.Close()
	}
}

// requireAuth returns an error if the user is not logged in.
func (e *env) requireAuth() error {
	if e.cfg.Token == "" {
		return errors.New("client: not logged in; run `client login`")
	}
	return nil
}

func openStore() (*localstore.Store, error) {
	path := os.Getenv("GOPHKEEPER_CLIENT_DB")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("client: home dir: %w", err)
		}
		path = filepath.Join(home, ".gophkeeper", "client.db")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("client: mkdir: %w", err)
	}
	return localstore.Open(path)
}

// ---- commands -----------------------------------------------------------

func runRegister(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("register", flag.ContinueOnError)
	fs.SetOutput(out)
	server := fs.String("server", "", "server base URL, e.g. http://localhost:8080")
	username := fs.String("username", "", "username")
	email := fs.String("email", "", "email")
	password := fs.String("password", "", "password (leave empty to be prompted)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	sc := bufio.NewScanner(in)
	if *server == "" {
		*server = prompt(sc, out, "Server URL", "http://localhost:8080")
	}
	if *username == "" {
		*username = prompt(sc, out, "Username", "")
	}
	if *email == "" {
		*email = prompt(sc, out, "Email", "")
	}
	if *password == "" {
		*password = prompt(sc, out, "Password", "")
	}

	cfg := &config.Config{ServerAddress: *server}
	client := api.New(*server, nil)
	if _, err := client.Register(ctx, *username, *email, *password); err != nil {
		return err
	}
	// Auto-login for convenience.
	token, uid, err := client.Login(ctx, *username, *password)
	if err != nil {
		return fmt.Errorf("client: register succeeded but login failed: %w", err)
	}
	cfg.Token, cfg.UserID = token, uid
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Fprintf(out, "Registered and logged in as %s (%s)\n", *username, uid)
	return nil
}

func runLogin(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.SetOutput(out)
	server := fs.String("server", "", "server base URL (defaults to saved value)")
	username := fs.String("username", "", "username")
	password := fs.String("password", "", "password (leave empty to be prompted)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *server != "" {
		cfg.ServerAddress = *server
	}
	if cfg.ServerAddress == "" {
		cfg.ServerAddress = prompt(bufio.NewScanner(in), out, "Server URL", "http://localhost:8080")
	}

	sc := bufio.NewScanner(in)
	if *username == "" {
		*username = prompt(sc, out, "Username", "")
	}
	if *password == "" {
		*password = prompt(sc, out, "Password", "")
	}

	client := api.New(cfg.ServerAddress, nil)
	token, uid, err := client.Login(ctx, *username, *password)
	if err != nil {
		return err
	}
	cfg.Token, cfg.UserID = token, uid
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Fprintf(out, "Logged in as %s (%s)\n", *username, uid)
	return nil
}

func runLogout(_ context.Context, out io.Writer) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := cfg.Clear(); err != nil {
		return err
	}
	fmt.Fprintln(out, "Logged out.")
	return nil
}

func runSync(ctx context.Context, out io.Writer) error {
	e, err := openEnv()
	if err != nil {
		return err
	}
	defer e.close()
	if err := e.requireAuth(); err != nil {
		return err
	}
	stats, err := sync.New(e.store, e.api, e.cfg.Token).Sync(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Synced: pushed=%d pulled=%d conflicts=%d\n",
		stats.Pushed, stats.Pulled, stats.Conflicts)
	return nil
}

func runAdd(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: client add <credentials|text|binary|card> [flags]")
	}
	typ := models.ItemType(args[0])
	if !typ.Valid() {
		return fmt.Errorf("unknown item type %q", args[0])
	}

	fs := flag.NewFlagSet("add", flag.ContinueOnError)
	fs.SetOutput(out)
	titleFlag := fs.String("title", "", "item title (prompted if empty)")
	metaFlag := fs.String("metadata", "", "free-form metadata")
	fileFlag := fs.String("from-file", "",
		"read item body from this file (required for binary items)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	e, err := openEnv()
	if err != nil {
		return err
	}
	defer e.close()

	var body []byte
	switch {
	case *fileFlag != "":
		body, err = os.ReadFile(*fileFlag)
		if err != nil {
			return fmt.Errorf("client: read %s: %w", *fileFlag, err)
		}
	case typ == models.ItemTypeBinary:
		return errors.New("client: binary items require -from-file <path>")
	}

	sc := bufio.NewScanner(in)
	title := *titleFlag
	if title == "" {
		title = prompt(sc, out, "Title", "")
	}
	metadata := *metaFlag
	if metadata == "" {
		metadata = prompt(sc, out, "Metadata (optional)", "")
	}
	if body == nil {
		s := prompt(sc, out, "Data", "")
		body = []byte(s)
	}

	it := &models.Item{
		ID:        uuid.NewString(),
		Type:      typ,
		Title:     title,
		Data:      body,
		Metadata:  metadata,
		UpdatedAt: time.Now().UTC(),
	}
	if err := e.store.UpsertItem(ctx, it, true); err != nil {
		return err
	}
	fmt.Fprintf(out, "Added %s item %s\n", typ, it.ID)
	return nil
}

func runList(ctx context.Context, args []string, out io.Writer) error {
	e, err := openEnv()
	if err != nil {
		return err
	}
	defer e.close()

	var filter models.ItemType
	if len(args) > 0 {
		filter = models.ItemType(args[0])
		if !filter.Valid() {
			return fmt.Errorf("unknown item type %q", args[0])
		}
	}
	items, err := e.store.ListItems(ctx)
	if err != nil {
		return err
	}
	n := 0
	for i := range items {
		if filter != "" && items[i].Type != filter {
			continue
		}
		printItem(out, &items[i])
		n++
	}
	fmt.Fprintf(out, "Total: %d\n", n)
	return nil
}

func runGet(ctx context.Context, args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: client get <id>")
	}
	e, err := openEnv()
	if err != nil {
		return err
	}
	defer e.close()

	it, _, err := e.store.GetItem(ctx, args[0])
	if err != nil {
		return err
	}
	printItem(out, it)
	return nil
}

func runDelete(ctx context.Context, args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: client delete <id>")
	}
	e, err := openEnv()
	if err != nil {
		return err
	}
	defer e.close()

	it, _, err := e.store.GetItem(ctx, args[0])
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if !now.After(it.UpdatedAt) {
		now = it.UpdatedAt.Add(time.Nanosecond)
	}
	it.DeletedAt = &now
	it.UpdatedAt = now
	if err := e.store.UpsertItem(ctx, it, true); err != nil {
		return err
	}
	fmt.Fprintf(out, "Deleted %s (run `client sync` to propagate)\n", args[0])
	return nil
}

func printItem(w io.Writer, it *models.Item) {
	status := "live"
	if it.DeletedAt != nil {
		status = "deleted"
	}
	fmt.Fprintf(w, "%s  [%s] %s  v%d  %s\n",
		it.ID, it.Type, it.Title, it.Version, status)
	if len(it.Metadata) > 0 {
		fmt.Fprintf(w, "  metadata: %s\n", it.Metadata)
	}
	if len(it.Data) > 0 {
		fmt.Fprintf(w, "  data: %s\n", string(it.Data))
	}
}

func prompt(sc *bufio.Scanner, out io.Writer, label, def string) string {
	if def != "" {
		fmt.Fprintf(out, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(out, "%s: ", label)
	}
	if !sc.Scan() {
		return def
	}
	v := strings.TrimSpace(sc.Text())
	if v == "" {
		return def
	}
	return v
}
