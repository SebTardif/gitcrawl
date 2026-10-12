package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/openclaw/gitcrawl/internal/store"
)

func TestInitCreatesMissingDatabaseInsideGitWorktree(t *testing.T) {
	ctx := context.Background()

	t.Run("db flag", func(t *testing.T) {
		repo := initOrdinaryGitRepo(t, ctx)
		dbPath := filepath.Join(repo, "gitcrawl.db")
		assertOrdinaryGitWorktree(t, ctx, repo, dbPath)
		configPath := filepath.Join(repo, "config.toml")
		if err := New().Run(ctx, []string{"--config", configPath, "init", "--db", dbPath}); err != nil {
			t.Fatalf("init: %v", err)
		}
		assertInitializedLocalDatabase(t, ctx, dbPath)
		if err := New().Run(ctx, []string{"--config", configPath, "doctor"}); err != nil {
			t.Fatalf("doctor: %v", err)
		}
	})

	t.Run("runtime dir", func(t *testing.T) {
		repo := initOrdinaryGitRepo(t, ctx)
		runtimeDir := filepath.Join(repo, "runtime")
		dbPath := filepath.Join(runtimeDir, "gitcrawl.db")
		assertOrdinaryGitWorktree(t, ctx, repo, dbPath)
		configPath := filepath.Join(repo, "config.toml")
		if err := New().Run(ctx, []string{"--config", configPath, "init", "--runtime-dir", runtimeDir}); err != nil {
			t.Fatalf("init: %v", err)
		}
		assertInitializedLocalDatabase(t, ctx, dbPath)
		if err := New().Run(ctx, []string{"--config", configPath, "doctor"}); err != nil {
			t.Fatalf("doctor: %v", err)
		}
	})
}

func TestInitLeavesExistingWorktreeDatabaseUntouched(t *testing.T) {
	ctx := context.Background()
	repo := initOrdinaryGitRepo(t, ctx)
	dbPath := filepath.Join(repo, "gitcrawl.db")
	assertOrdinaryGitWorktree(t, ctx, repo, dbPath)
	const sentinel = "not a gitcrawl database\n"
	if err := os.WriteFile(dbPath, []byte(sentinel), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	configPath := filepath.Join(repo, "config.toml")
	if err := New().Run(ctx, []string{"--config", configPath, "init", "--db", dbPath}); err != nil {
		t.Fatalf("init: %v", err)
	}
	got, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read database: %v", err)
	}
	if string(got) != sentinel {
		t.Fatalf("existing worktree database changed to %q", got)
	}
}

func TestInitDBPreservesCompressedPortableCheckout(t *testing.T) {
	fixture := newPortableRefreshFixture(t, true)
	logical := filepath.Join(fixture.checkout, fixture.relative)
	if _, err := os.Stat(logical); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fixture raw database: %v", err)
	}
	before := portableTestGit(t, fixture.checkout, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored=matching")
	if before != "" {
		t.Fatalf("fixture not clean: %q", before)
	}
	configPath := filepath.Join(filepath.Dir(fixture.checkout), "local.toml")
	app := New()
	var stdout, stderr bytes.Buffer
	app.Stdout, app.Stderr = &stdout, &stderr
	if err := app.Run(context.Background(), []string{"--config", configPath, "init", "--db", logical}); err != nil {
		t.Fatalf("init: %v\n%s", err, stderr.String())
	}
	if _, err := os.Stat(logical); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("init created raw database: %v", err)
	}
	after := portableTestGit(t, fixture.checkout, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored=matching")
	if after != "" {
		t.Fatalf("init dirtied compressed checkout: %q", after)
	}
	result, err := fixture.refresh(t)
	if err != nil {
		t.Fatalf("refresh: %+v %v", result, err)
	}
}

func initOrdinaryGitRepo(t *testing.T, ctx context.Context) string {
	t.Helper()
	repo := t.TempDir()
	if err := runGit(ctx, repo, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	return repo
}

func assertOrdinaryGitWorktree(t *testing.T, ctx context.Context, repo, dbPath string) {
	t.Helper()
	root, portable, err := portableStoreRoot(ctx, dbPath)
	if err != nil {
		t.Fatalf("portable store root: %v", err)
	}
	if !portable || root != repo {
		t.Fatalf("portable store root = %q, portable=%v, want ordinary worktree %q", root, portable, repo)
	}
}

func assertInitializedLocalDatabase(t *testing.T, ctx context.Context, dbPath string) {
	t.Helper()
	st, err := store.OpenReadOnly(ctx, dbPath)
	if err != nil {
		t.Fatalf("open initialized database: %v", err)
	}
	if _, err := st.Status(ctx); err != nil {
		t.Fatalf("read initialized database: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close initialized database: %v", err)
	}
}
