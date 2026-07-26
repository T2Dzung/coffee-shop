package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Request struct {
	Name       string
	Args       []string
	Dir        string
	Env        map[string]string
	Stdin      io.Reader
	Timeout    time.Duration
	Stream     bool
	Redactions []string
}

type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Elapsed  time.Duration
}

type Runner interface {
	Run(context.Context, Request) (Result, error)
}

type OSRunner struct {
	Stdout io.Writer
	Stderr io.Writer
}

func (r OSRunner) Run(ctx context.Context, request Request) (Result, error) {
	if request.Name == "" {
		return Result{}, fmt.Errorf("command name is required")
	}
	if request.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, request.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, request.Name, request.Args...)
	cmd.Dir = request.Dir
	cmd.Stdin = request.Stdin
	cmd.Env = mergeEnv(os.Environ(), request.Env)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Never stream values that require redaction: MultiWriter would expose the
	// raw bytes before redact() sees them. Buffer first, then emit the sanitized
	// result after the process exits.
	safeLiveStream := request.Stream && len(request.Redactions) == 0
	if safeLiveStream {
		if r.Stdout != nil {
			cmd.Stdout = io.MultiWriter(&stdout, r.Stdout)
		}
		if r.Stderr != nil {
			cmd.Stderr = io.MultiWriter(&stderr, r.Stderr)
		}
	}

	started := time.Now()
	err := cmd.Run()
	result := Result{
		Stdout:  redact(stdout.String(), request.Redactions),
		Stderr:  redact(stderr.String(), request.Redactions),
		Elapsed: time.Since(started),
	}
	if request.Stream && !safeLiveStream {
		if r.Stdout != nil {
			_, _ = io.WriteString(r.Stdout, result.Stdout)
		}
		if r.Stderr != nil {
			_, _ = io.WriteString(r.Stderr, result.Stderr)
		}
	}
	if err == nil {
		return result, nil
	}
	if ctx.Err() != nil {
		return result, fmt.Errorf("%s timed out or was cancelled: %w", request.Name, ctx.Err())
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return result, fmt.Errorf("start %s: %w", request.Name, err)
	}
	result.ExitCode = exitErr.ExitCode()
	return result, fmt.Errorf("%s exited with code %d: %s", request.Name, result.ExitCode, strings.TrimSpace(result.Stderr))
}

func mergeEnv(base []string, overrides map[string]string) []string {
	values := make(map[string]string, len(base)+len(overrides))
	for _, item := range base {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	for key, value := range overrides {
		values[key] = value
	}
	result := make([]string, 0, len(values))
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	return result
}

func redact(value string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, "[REDACTED]")
		}
	}
	return value
}
