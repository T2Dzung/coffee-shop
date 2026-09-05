package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/thangchung/go-coffeeshop/internal/platformctl/lab"
)

// Offline only: never starts a playground, commits source or calls Kubernetes.
func renderLabGitOps(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("lab iximiuz gitops-render", flag.ContinueOnError)
	flags.SetOutput(stderr)
	run := flags.String("run", "", "exact run owning the image locks")
	playground := flags.String("playground", "", "exact template name")
	revision := flags.String("revision", "", "full published Git commit SHA")
	core := flags.String("core-lock", "", "local copy of runtime-core/kustomization.yaml")
	orders := flags.String("orders-lock", "", "local copy of runtime-orders/kustomization.yaml")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *core == "" || *orders == "" {
		return fmt.Errorf("provide --core-lock and --orders-lock; no positional arguments")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	cfg, err := lab.Load(root, lab.Options{RunID: *run, Playground: *playground, Binary: "labctl", Machine: "dev-machine", Workspace: "/home/laborant/coffeeshop-core", Lifetime: time.Hour})
	if err != nil {
		return err
	}
	coreData, err := os.ReadFile(*core)
	if err != nil {
		return err
	}
	ordersData, err := os.ReadFile(*orders)
	if err != nil {
		return err
	}
	result, err := lab.RenderGitOps(cfg, *revision, coreData, ordersData)
	if err != nil {
		return err
	}
	_, err = stdout.Write(result)
	return err
}
