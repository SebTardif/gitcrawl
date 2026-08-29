package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSyncPortableStoreDirtyPullSurfacesResetError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("git PATH wrapper is a POSIX script")
	}
	ctx := context.Background()
	remoteDir, checkoutDir := seedPortableStoreRepo(t)
	if _, err := syncPortableStore(ctx, remoteDir, checkoutDir); err != nil {
		t.Fatalf("initial portable sync: %v", err)
	}

	incoming := "incoming.txt"
	if err := os.WriteFile(filepath.Join(remoteDir, incoming), []byte("remote-incoming\n"), 0o644); err != nil {
		t.Fatalf("write remote incoming: %v", err)
	}
	if err := runGit(ctx, remoteDir, "add", incoming); err != nil {
		t.Fatalf("git add incoming: %v", err)
	}
	if err := runGit(ctx, remoteDir, "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-m", "add incoming"); err != nil {
		t.Fatalf("git commit incoming: %v", err)
	}
	if err := os.WriteFile(filepath.Join(checkoutDir, incoming), []byte("local-untracked\n"), 0o644); err != nil {
		t.Fatalf("write untracked incoming: %v", err)
	}
	if !gitWorktreeClean(ctx, checkoutDir) {
		t.Fatal("precondition: worktree must look clean so the dirty-pull reset path is used")
	}

	installGitResetFailureWrapper(t, "test-reset-hard-denied")

	_, err := syncPortableStore(ctx, remoteDir, checkoutDir)
	if err == nil {
		t.Fatal("expected reset --hard failure after dirty pull")
	}
	msg := err.Error()
	if !strings.Contains(msg, "test-reset-hard-denied") {
		t.Fatalf("error = %q, want the reset failure, not the original dirty pull error", msg)
	}
	if strings.Contains(msg, "would be overwritten") || strings.Contains(msg, "Your local changes") {
		t.Fatalf("returned dirty pull error instead of reset error: %q", msg)
	}
}

func TestSyncPortableStoreInitCloneUsesRepairTimeout(t *testing.T) {
	src, err := os.ReadFile("app.go")
	if err != nil {
		t.Fatalf("read app.go: %v", err)
	}
	body, ok := functionSource(string(src), "syncPortableStore")
	if !ok {
		t.Fatal("syncPortableStore not found in app.go")
	}
	timeoutCall := "context.WithTimeout(ctx, portableStoreRepairTimeout)"
	timeoutIdx := strings.Index(body, timeoutCall)
	if timeoutIdx < 0 {
		t.Fatal("init clone must wrap git clone with context.WithTimeout(ctx, portableStoreRepairTimeout)")
	}
	cloneIdx := strings.Index(body, `"clone"`)
	if cloneIdx < 0 {
		t.Fatal("syncPortableStore must invoke git clone")
	}
	if timeoutIdx > cloneIdx {
		t.Fatal("portableStoreRepairTimeout must be applied before the init clone")
	}
	if !strings.Contains(body[timeoutIdx:cloneIdx], "cloneCtx") && !strings.Contains(body, "runGit(cloneCtx") {
		t.Fatal("init clone must run under the timeout context, like runtime.go reclone")
	}
}

func TestSyncPortableStoreInitCloneHonorsParentDeadline(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("git PATH wrapper is a POSIX script")
	}
	installGitHangOnCloneWrapper(t)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := syncPortableStore(ctx, "https://example.invalid/gitcrawl-store.git", filepath.Join(t.TempDir(), "checkout"))
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected clone to fail when the parent context expires")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("clone was not cancelled after parent deadline, elapsed %s", elapsed)
	}
	if !strings.Contains(err.Error(), "deadline") && !strings.Contains(err.Error(), "context") {
		t.Fatalf("clone error = %q, want a context deadline", err)
	}
}

func seedPortableStoreRepo(t *testing.T) (remoteDir, checkoutDir string) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	remoteDir = filepath.Join(dir, "remote")
	checkoutDir = filepath.Join(dir, "checkout")
	if err := os.MkdirAll(filepath.Join(remoteDir, "data"), 0o755); err != nil {
		t.Fatalf("mkdir remote: %v", err)
	}
	if err := runGit(ctx, remoteDir, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	dbPath := filepath.Join(remoteDir, "data", "openclaw__openclaw.sync.db")
	if err := os.WriteFile(dbPath, []byte("remote-v1"), 0o644); err != nil {
		t.Fatalf("write remote db: %v", err)
	}
	if err := runGit(ctx, remoteDir, "add", "data/openclaw__openclaw.sync.db"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if err := runGit(ctx, remoteDir, "-c", "user.email=test@example.com", "-c", "user.name=Test", "commit", "-m", "seed store"); err != nil {
		t.Fatalf("git commit seed: %v", err)
	}
	return remoteDir, checkoutDir
}

func installGitResetFailureWrapper(t *testing.T, message string) {
	t.Helper()
	installGitArgFailureWrapper(t, "reset", message)
}

func installGitHangOnCloneWrapper(t *testing.T) {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("look up git: %v", err)
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"for arg in \"$@\"; do\n" +
		"  if [ \"$arg\" = \"clone\" ]; then\n" +
		"    sleep 30\n" +
		"    exit 1\n" +
		"  fi\n" +
		"done\n" +
		"exec \"" + realGit + "\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatalf("write hanging git wrapper: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func installGitArgFailureWrapper(t *testing.T, verb, message string) {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("look up git: %v", err)
	}
	dir := t.TempDir()
	script := "#!/bin/sh\n" +
		"for arg in \"$@\"; do\n" +
		"  if [ \"$arg\" = \"" + verb + "\" ]; then\n" +
		"    echo \"" + message + "\" >&2\n" +
		"    exit 128\n" +
		"  fi\n" +
		"done\n" +
		"exec \"" + realGit + "\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatalf("write git wrapper: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func functionSource(src, name string) (string, bool) {
	sig := "func " + name + "("
	start := strings.Index(src, sig)
	if start < 0 {
		return "", false
	}
	rest := src[start:]
	next := strings.Index(rest[len(sig):], "\nfunc ")
	if next < 0 {
		return rest, true
	}
	return rest[:len(sig)+next], true
}
