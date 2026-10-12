package scripts_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"

	"github.com/amascii/vinance/internal/testutil"
)

// env is a sandbox for service.sh: a fake $HOME, a seeded source DB, and launchd switched off.
type env struct {
	home, sourceDB, hash, addr string
}

func newEnv(t *testing.T) env {
	t.Helper()
	for _, tool := range []string{"bash", "sqlite3", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not installed", tool)
		}
	}
	// Seed a real, migrated database with one account in it.
	conn := testutil.NewDB(t)
	testutil.SeedAccount(t, conn, "Seeded", "asset", "USD")
	src := filepath.Join(t.TempDir(), "source.db")
	_, err := conn.Exec(`VACUUM INTO ?`, src)
	require.NoError(t, err)

	h, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	require.NoError(t, err)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return env{home: t.TempDir(), sourceDB: src, hash: string(h), addr: addr}
}

func (e env) run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("bash", append([]string{"service.sh"}, args...)...)
	cmd.Env = append(os.Environ(),
		"VINANCE_SERVICE_HOME="+e.home,
		"VINANCE_SERVICE_SOURCE_DB="+e.sourceDB,
		"VINANCE_SERVICE_ADDR="+e.addr,
		"VINANCE_SERVICE_NO_LAUNCHD=1",
		"VINANCE_PASSWORD_HASH="+e.hash,
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (e env) path(parts ...string) string {
	return filepath.Join(append([]string{e.home}, parts...)...)
}

func (e env) data(parts ...string) string {
	return e.path(append([]string{"Library", "Application Support", "vinance"}, parts...)...)
}

func (e env) plist() string { return e.path("Library", "LaunchAgents", "com.amascii.vinance.plist") }

func TestInstallWritesTheServiceOutsideTheRepo(t *testing.T) {
	e := newEnv(t)
	out, err := e.run(t, "install")
	require.NoError(t, err, out)

	assert.FileExists(t, e.data("bin", "vinance"))
	info, err := os.Stat(e.plist())
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "the plist holds the password hash")

	plist, err := os.ReadFile(e.plist())
	require.NoError(t, err)
	for _, want := range []string{e.hash, e.data("vinance.db"), e.addr, e.data("bin", "vinance"),
		"<key>VINANCE_SECURE_COOKIES</key><string>true</string>", "<key>KeepAlive</key><true/>"} {
		assert.Contains(t, string(plist), want)
	}
	if _, err := exec.LookPath("plutil"); err == nil {
		lint, err := exec.Command("plutil", "-lint", e.plist()).CombinedOutput()
		assert.NoError(t, err, string(lint))
	}

	// The DB is a copy, and the original is left alone.
	assert.FileExists(t, e.data("vinance.db"))
	assert.FileExists(t, e.sourceDB)
	assert.Equal(t, 1, countAccounts(t, e.data("vinance.db"), "Seeded"))
}

func TestInstalledBinaryServesWithALogin(t *testing.T) {
	e := newEnv(t)
	out, err := e.run(t, "install")
	require.NoError(t, err, out)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, e.data("bin", "vinance"), "serve")
	cmd.Env = append(os.Environ(), "VINANCE_DB="+e.data("vinance.db"), "VINANCE_ADDR="+e.addr, "VINANCE_PASSWORD_HASH="+e.hash)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { cancel(); _ = cmd.Wait() })

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	require.Eventually(t, func() bool {
		resp, err := client.Get("http://" + e.addr + "/healthz")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 10*time.Second, 100*time.Millisecond)

	resp, err := client.Get("http://" + e.addr + "/")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusSeeOther, resp.StatusCode)
	assert.Equal(t, "/login", resp.Header.Get("Location"))
}

func TestDryRunWritesNothing(t *testing.T) {
	e := newEnv(t)
	out, err := e.run(t, "install", "--dry-run")
	require.NoError(t, err, out)
	assert.Contains(t, out, "[dry-run]")

	assert.NoFileExists(t, e.plist())
	assert.NoFileExists(t, e.data("vinance.db"))
	assert.NoFileExists(t, e.data("bin", "vinance"))
}

func TestReinstallKeepsTheLiveDatabase(t *testing.T) {
	e := newEnv(t)
	_, err := e.run(t, "install")
	require.NoError(t, err)

	// The service has since written something; a second install (to change the plist) must not overwrite it with the source copy.
	out, err := exec.Command("sqlite3", e.data("vinance.db"), "UPDATE accounts SET name = 'Edited live' WHERE name = 'Seeded'").CombinedOutput()
	require.NoError(t, err, string(out))

	text, err := e.run(t, "install")
	require.NoError(t, err, text)
	assert.Contains(t, text, "keeping the existing database")
	assert.Equal(t, 1, countAccounts(t, e.data("vinance.db"), "Edited live"))
}

func TestInstallRejectsAnythingButABcryptHash(t *testing.T) {
	e := newEnv(t)
	e.hash = "hunter2"
	out, err := e.run(t, "install")
	require.Error(t, err)
	assert.Contains(t, out, "not a bcrypt hash")
	assert.NoFileExists(t, e.plist())
}

func TestDeployNeedsAnInstall(t *testing.T) {
	e := newEnv(t)
	out, err := e.run(t, "deploy")
	require.Error(t, err)
	assert.Contains(t, out, "not installed")
}

func TestUninstallRemovesTheAgentButKeepsData(t *testing.T) {
	e := newEnv(t)
	_, err := e.run(t, "install")
	require.NoError(t, err)

	out, err := e.run(t, "uninstall")
	require.NoError(t, err, out)
	assert.NoFileExists(t, e.plist())
	assert.FileExists(t, e.data("vinance.db"))
}

func countAccounts(t *testing.T, dbPath, name string) int {
	t.Helper()
	out, err := exec.Command("sqlite3", dbPath, fmt.Sprintf("SELECT COUNT(*) FROM accounts WHERE name = '%s'", name)).CombinedOutput()
	require.NoError(t, err, string(out))
	var n int
	_, err = fmt.Sscan(string(out), &n)
	require.NoError(t, err)
	return n
}
