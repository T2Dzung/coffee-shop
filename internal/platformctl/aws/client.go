package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
)

type Client struct {
	Runner  command.Runner
	Region  string
	Timeout time.Duration
}

func (c Client) Text(ctx context.Context, args ...string) (string, error) {
	result, err := c.run(ctx, args...)
	return strings.TrimSpace(result.Stdout), err
}

func (c Client) JSON(ctx context.Context, target any, args ...string) error {
	result, err := c.run(ctx, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(result.Stdout), target); err != nil {
		return fmt.Errorf("decode AWS CLI JSON for %v: %w", args, err)
	}
	return nil
}

func (c Client) Run(ctx context.Context, args ...string) error {
	_, err := c.run(ctx, args...)
	return err
}

func (c Client) run(ctx context.Context, args ...string) (command.Result, error) {
	env := map[string]string{}
	if c.Region != "" {
		env["AWS_REGION"] = c.Region
		env["AWS_DEFAULT_REGION"] = c.Region
	}
	return c.Runner.Run(ctx, command.Request{
		Name: "aws", Args: args, Env: env, Timeout: c.Timeout,
	})
}
