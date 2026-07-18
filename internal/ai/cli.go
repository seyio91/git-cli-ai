package ai

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// cliGenerator runs an external tool that already holds its own credentials.
// command is an argv array executed directly — never through a shell — so a
// path or argument containing spaces, quotes or a semicolon cannot become a
// second command.
type cliGenerator struct {
	name    string
	command []string
}

// commandTimeout bounds an external tool that never exits. It is generous
// because a cli provider is usually a full agent doing real work, but it is
// finite: an agent hot path must not be able to block forever, and the one
// retry would otherwise grant a second unbounded turn.
const commandTimeout = 3 * time.Minute

func (g cliGenerator) Generate(ctx context.Context, req GenRequest) (GenResult, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, g.command[0], g.command[1:]...)
	cmd.Stdin = strings.NewReader(req.Prompt())

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return GenResult{}, &ProviderError{
				Provider: g.name,
				Message:  fmt.Sprintf("ai provider %q timed out after %s", g.name, commandTimeout),
				Hint:     fmt.Sprintf("%s did not finish; check that it reads the prompt from stdin and exits, or supply --message to commit without a provider call", g.command[0]),
			}
		}

		return GenResult{}, &ProviderError{
			Provider: g.name,
			Message:  fmt.Sprintf("ai provider %q failed: %s", g.name, g.command[0]),
			Hint:     g.failureHint(err),
			Details:  details(strings.TrimSpace(stderr.String()), err),
		}
	}

	message := strings.TrimSpace(stdout.String())
	if message == "" {
		return GenResult{}, &ProviderError{
			Provider: g.name,
			Message:  fmt.Sprintf("ai provider %q returned an empty message", g.name),
			Hint:     fmt.Sprintf("%s must print the commit message on stdout; run it with the prompt on stdin to see what it emits", g.command[0]),
			Details:  strings.TrimSpace(stderr.String()),
		}
	}

	return GenResult{Message: message}, nil
}

// failureHint names the program but never its arguments: a command line is a
// place people put tokens, and a hint is printed on every failure.
func (g cliGenerator) failureHint(err error) string {
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Sprintf("%s could not be executed; check that it is installed, executable, and on PATH", g.command[0])
	}
	return fmt.Sprintf("run %s manually with the prompt on stdin to see its full output", g.command[0])
}

func details(stderr string, err error) string {
	if stderr != "" {
		return stderr
	}
	return err.Error()
}
