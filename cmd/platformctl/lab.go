package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/thangchung/go-coffeeshop/internal/platformctl/command"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/evidence"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/iximiuz"
	"github.com/thangchung/go-coffeeshop/internal/platformctl/lab"
)

func runLab(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) >= 2 && args[0] == "iximiuz" && args[1] == "gitops-render" {
		return renderLabGitOps(args[2:], stdout, stderr)
	}
	if len(args) < 2 || args[0] != "iximiuz" || (args[1] != "setup" && args[1] != "resume" && args[1] != "status" && args[1] != "stop" && args[1] != "stateful" && args[1] != "orders") {
		return fmt.Errorf("usage: platformctl lab iximiuz <setup|stateful|orders|resume|status|stop|gitops-render>; gitops-render --help describes offline Application rendering")
	}
	flags := flag.NewFlagSet("lab iximiuz "+args[1], flag.ContinueOnError)
	flags.SetOutput(stderr)
	var options lab.Options
	options.Create = args[1] == "setup"
	flags.StringVar(&options.RunID, "run", "", "exact existing playground run ID; never auto-select latest")
	flags.StringVar(&options.Playground, "playground", "", "exact expected custom playground name")
	flags.StringVar(&options.Binary, "labctl", "labctl", "labctl binary path")
	flags.StringVar(&options.Machine, "machine", "dev-machine", "builder/control client VM")
	flags.StringVar(&options.Workspace, "workspace", "/home/laborant/coffeeshop-core", "lab source and runtime lock directory; setup creates it")
	flags.DurationVar(&options.Lifetime, "lifetime", time.Hour, "after create/restart: total session duration, 5m to 3h; existing running sessions keep their deadline")
	defaultTimeout := 10 * time.Minute
	if options.Create || args[1] == "stateful" || args[1] == "orders" {
		defaultTimeout = 45 * time.Minute
	}
	timeout := flags.Duration("timeout", defaultTimeout, "overall command timeout (setup/stateful: 45m, other actions: 10m)")
	output := flags.String("evidence", "", "optional structured evidence JSON output")
	if err := flags.Parse(args[2:]); err != nil {
		return err
	}
	if flags.NArg() != 0 || *timeout <= 0 {
		return fmt.Errorf("unexpected positional arguments or invalid timeout")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, err := lab.Load(root, options)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	recorder := evidence.New("lab-iximiuz-" + args[1])
	engine := lab.Engine{Config: cfg, Output: stdout, Evidence: recorder,
		Client: iximiuz.Client{Runner: command.OSRunner{}, Binary: cfg.Binary, RunID: cfg.RunID, Machine: cfg.Machine}}
	runErr := engine.Run(ctx, args[1])
	if *output != "" {
		if err := evidence.WriteAtomic(*output, recorder.Snapshot()); err != nil {
			if runErr != nil {
				return fmt.Errorf("%w; writing evidence: %v", runErr, err)
			}
			return err
		}
	}
	return runErr
}
