package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/Berthelmaster/pi-sens/internal/auth"
)

// maxPasswordInput bounds how much of stdin hash-password reads. bcrypt only
// takes 72 bytes, so anything near this is a mistake such as piping a file.
const maxPasswordInput = 4096

const hashPasswordUsage = `Usage: pisens hash-password

Reads a password from stdin and prints its bcrypt hash for --auth-password-hash
or PISENS_AUTH_PASSWORD_HASH. On a terminal it asks for the password twice
without echoing it. Otherwise it reads stdin, without one trailing newline:

  printf '%s\n' 'correct horse' | pisens hash-password
`

// runHashPassword implements "pisens hash-password" and returns the exit code.
func runHashPassword(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) > 0 {
		if args[0] == "-h" || args[0] == "-help" || args[0] == "--help" {
			_, _ = io.WriteString(stderr, hashPasswordUsage)
			return 0
		}
		fmt.Fprintf(stderr, "pisens hash-password: unexpected arguments: %v\n\n%s", args, hashPasswordUsage)
		return 2
	}

	var password string
	var err error
	if f, ok := stdin.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		password, err = promptPassword(int(f.Fd()), stderr)
	} else {
		password, err = readPassword(stdin)
	}
	if err != nil {
		fmt.Fprintln(stderr, "pisens hash-password:", err)
		return 1
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		fmt.Fprintln(stderr, "pisens hash-password:", err)
		return 1
	}
	fmt.Fprintln(stdout, hash)
	return 0
}

// promptPassword asks for the password twice on the terminal fd without
// echoing it.
func promptPassword(fd int, prompts io.Writer) (string, error) {
	read := func(prompt string) (string, error) {
		fmt.Fprint(prompts, prompt)
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(prompts)
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		return string(b), nil
	}
	password, err := read("Password: ")
	if err != nil {
		return "", err
	}
	again, err := read("Repeat password: ")
	if err != nil {
		return "", err
	}
	if password != again {
		return "", errors.New("the passwords do not match")
	}
	return password, nil
}

// readPassword reads a password from a pipe or file. One trailing newline is
// dropped, so echo and printf '%s\n' both work; anything else, spaces
// included, is part of the password.
func readPassword(r io.Reader) (string, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxPasswordInput+1))
	if err != nil {
		return "", fmt.Errorf("read password: %w", err)
	}
	if len(b) > maxPasswordInput {
		return "", fmt.Errorf("password input is longer than %d bytes", maxPasswordInput)
	}
	b = bytes.TrimSuffix(b, []byte("\n"))
	b = bytes.TrimSuffix(b, []byte("\r"))
	password := string(b)
	// A browser login prompt cannot send a line break, so a password with one
	// could never be used. It is most likely more than one line of input.
	if strings.ContainsAny(password, "\r\n") {
		return "", errors.New("the password must be a single line")
	}
	return password, nil
}
