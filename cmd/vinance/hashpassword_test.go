package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// stdinWith returns a pipe (not a terminal) pre-filled with s.
func stdinWith(t *testing.T, s string) *os.File {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	_, err = w.WriteString(s)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	t.Cleanup(func() { r.Close() })
	return r
}

func TestHashPasswordFromStdin(t *testing.T) {
	var out, errOut bytes.Buffer
	require.NoError(t, hashPassword(stdinWith(t, "s3cret pass\n"), &out, &errOut))

	hash := strings.TrimSpace(out.String())
	assert.NoError(t, bcrypt.CompareHashAndPassword([]byte(hash), []byte("s3cret pass")), "the trailing newline is not part of the password")
	assert.Empty(t, errOut.String(), "nothing but the hash is printed when piped")
}

func TestHashPasswordRejectsEmpty(t *testing.T) {
	var out bytes.Buffer
	assert.ErrorContains(t, hashPassword(stdinWith(t, "\n"), &out, &out), "empty")
	assert.Error(t, hashPassword(stdinWith(t, ""), &out, &out))
	assert.Empty(t, out.String())
}
