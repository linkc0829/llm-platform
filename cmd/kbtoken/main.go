package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/auth"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/config"
	"github.com/linkc0829/go-knowledge-base-qa-bot/internal/platform/logger"
)

const cliActorID = "kbtoken"

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "kbtoken:", err)
		os.Exit(1)
	}
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return errors.New("command is required: list, create-admin, rotate-admin, or revoke-admin")
	}

	cfg, err := config.LoadAuth()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	lg, err := logger.New(logger.Config{
		Level:    cfg.Logger.Level,
		Encoding: cfg.Logger.Encoding,
		Output:   logger.WithoutStdout(cfg.Logger.Output),
	})
	if err != nil {
		return fmt.Errorf("logger: %w", err)
	}
	defer func() { _ = lg.Sync() }()

	if err := ensureServerStopped(cfg); err != nil {
		return err
	}

	switch args[0] {
	case "list":
		if len(args) != 1 {
			return errors.New("list does not accept positional arguments")
		}
		return list(cfg, lg, stdout)
	case "create-admin":
		return createAdmin(cfg, lg, args[1:], stdout, stderr)
	case "rotate-admin":
		return rotateAdmin(cfg, lg, args[1:], stdout, stderr)
	case "revoke-admin":
		return revokeAdmin(cfg, lg, args[1:], stdout, stderr)
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func list(cfg *config.Config, lg *zap.Logger, stdout io.Writer) error {
	store, err := auth.LoadBootstrapFile(cfg.Auth.File, lg)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	records, err := store.ListTokens(context.Background())
	if err != nil {
		return fmt.Errorf("list tokens: %w", err)
	}
	result := make([]tokenMetadata, 0, len(records))
	for _, record := range records {
		result = append(result, toTokenMetadata(record))
	}
	return json.NewEncoder(stdout).Encode(result)
}

func createAdmin(cfg *config.Config, lg *zap.Logger, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("create-admin", flag.ContinueOnError)
	fs.SetOutput(stderr)
	name := fs.String("name", "", "admin principal name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("create-admin does not accept positional arguments")
	}
	if err := auth.ValidateName(*name); err != nil {
		return err
	}

	var store *auth.Store
	var err error
	_, statErr := os.Stat(cfg.Auth.File)
	if errors.Is(statErr, os.ErrNotExist) {
		store, err = auth.NewBootstrapStore(cfg.Auth.File, lg)
		if err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	} else if statErr != nil {
		return fmt.Errorf("stat auth file: %w", statErr)
	} else {
		store, err = auth.LoadBootstrapFile(cfg.Auth.File, lg)
		if err != nil {
			return fmt.Errorf("auth: %w", err)
		}
		records, err := store.ListTokens(context.Background())
		if err != nil {
			return fmt.Errorf("list tokens: %w", err)
		}
		if len(records) != 0 {
			return errors.New("auth file already has principals; use rotate-admin for an initialized store")
		}
	}
	record, token, err := store.CreateAdminToken(context.Background(), cliActorID, *name)
	if err != nil {
		return fmt.Errorf("create admin: %w", err)
	}
	return json.NewEncoder(stdout).Encode(createAdminResponse{ID: record.ID, Name: record.Name, Token: token})
}

func rotateAdmin(cfg *config.Config, lg *zap.Logger, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("rotate-admin", flag.ContinueOnError)
	fs.SetOutput(stderr)
	id := fs.String("id", "", "existing admin principal ID")
	name := fs.String("name", "", "new admin principal name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("rotate-admin does not accept positional arguments")
	}

	store, err := auth.LoadBootstrapFile(cfg.Auth.File, lg)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	records, err := store.ListTokens(context.Background())
	if err != nil {
		return fmt.Errorf("list tokens: %w", err)
	}
	var old auth.Record
	for _, record := range records {
		if record.ID == *id {
			old = record
			break
		}
	}
	if old.ID == "" {
		return auth.ErrPrincipalNotFound
	}
	if !old.Admin {
		return auth.ErrNotAdminPrincipal
	}
	newName := *name
	if newName == "" {
		newID, err := auth.GeneratePrincipalID()
		if err != nil {
			return err
		}
		newName = rotatedName(old.Name, newID)
	}
	if err := auth.ValidateName(newName); err != nil {
		return err
	}
	record, token, err := store.CreateAdminToken(context.Background(), cliActorID, newName)
	if err != nil {
		return fmt.Errorf("rotate admin: %w", err)
	}
	return json.NewEncoder(stdout).Encode(createAdminResponse{ID: record.ID, Name: record.Name, Token: token})
}

func revokeAdmin(cfg *config.Config, lg *zap.Logger, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("revoke-admin", flag.ContinueOnError)
	fs.SetOutput(stderr)
	id := fs.String("id", "", "admin principal ID")
	force := fs.Bool("force", false, "allow revoking the last admin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("revoke-admin does not accept positional arguments")
	}

	store, err := auth.LoadBootstrapFile(cfg.Auth.File, lg)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if err := store.DeleteAdminToken(context.Background(), cliActorID, *id, *force); err != nil {
		if errors.Is(err, auth.ErrLastAdminRequiresForce) {
			return fmt.Errorf("%w; rerun with -force if this is intentional", err)
		}
		return fmt.Errorf("revoke admin: %w", err)
	}
	_, err = fmt.Fprintln(stdout, *id)
	return err
}

func ensureServerStopped(cfg *config.Config) error {
	host := cfg.HTTP.BindAddress
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	url := "http://" + net.JoinHostPort(host, strconv.Itoa(cfg.HTTP.Port)) + "/health"
	client := &http.Client{Timeout: 500 * time.Millisecond}
	response, err := client.Get(url)
	if err != nil {
		// This check is intentionally only a friendly gate. The auth lock file
		// below is the correctness mechanism for concurrent writers.
		return nil //nolint:nilerr // an unreachable health endpoint is not proof that a writer is running
	}
	_ = response.Body.Close()
	return fmt.Errorf("knowledge-base service is running at %s; stop it before modifying auth.json", url)
}

func rotatedName(oldName, newID string) string {
	suffix := "-rotated-" + strings.ToLower(strings.TrimPrefix(newID, "p_"))
	maxBase := 64 - len(suffix)
	if len(oldName) > maxBase {
		oldName = oldName[:maxBase]
	}
	return oldName + suffix
}

type tokenMetadata struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Teams       []string  `json:"teams,omitempty"`
	AllTeams    bool      `json:"all_teams"`
	Engineering bool      `json:"engineering"`
	Indexer     bool      `json:"indexer"`
	Admin       bool      `json:"admin"`
	CreatedAt   time.Time `json:"created_at"`
}

type createAdminResponse struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Token string `json:"token"`
}

func toTokenMetadata(record auth.Record) tokenMetadata {
	return tokenMetadata{
		ID:          record.ID,
		Name:        record.Name,
		Teams:       append([]string(nil), record.Teams...),
		AllTeams:    record.AllTeams,
		Engineering: record.Engineering,
		Indexer:     record.Indexer,
		Admin:       record.Admin,
		CreatedAt:   record.CreatedAt,
	}
}
