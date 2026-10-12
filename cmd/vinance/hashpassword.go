package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/amascii/vinance/internal/web"
)

// hashPassword prints the bcrypt hash for VINANCE_PASSWORD_HASH. On a terminal it prompts (twice, without echo);
// otherwise it reads the password from the first line of stdin, so it can be scripted.
func hashPassword(stdin *os.File, stdout, stderr io.Writer) error {
	var pw string
	if term.IsTerminal(int(stdin.Fd())) {
		first, err := prompt(stdin, stderr, "Password: ")
		if err != nil {
			return err
		}
		again, err := prompt(stdin, stderr, "Again: ")
		if err != nil {
			return err
		}
		if first != again {
			return errors.New("passwords do not match")
		}
		pw = first
	} else {
		line, err := bufio.NewReader(stdin).ReadString('\n')
		if err != nil && line == "" {
			return fmt.Errorf("read password from stdin: %w", err)
		}
		pw = strings.TrimRight(line, "\r\n")
	}
	if pw == "" {
		return errors.New("password is empty")
	}
	h, err := web.HashPassword(pw)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(stdout, h)
	return err
}

func prompt(stdin *os.File, stderr io.Writer, label string) (string, error) {
	fmt.Fprint(stderr, label)
	b, err := term.ReadPassword(int(stdin.Fd()))
	fmt.Fprintln(stderr)
	return string(b), err
}
