package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/alexflint/go-arg"
	box "github.com/sagernet/sing-box"

	"github.com/getlantern/sing-box-extensions/protocol"
)

var args struct {
	DataDir    string `arg:"-d,--data-dir" help:"data directory" default:"./data"`
	ConfigFile string `arg:"-c,--config" help:"Path to JSON config file" default:"./config.json"`

	Run   *RunCmd   `arg:"subcommand:run" help:"start the server"`
	Check *CheckCmd `arg:"subcommand:check" help:"validate initial configuration"`
}

func newBaseContext() context.Context {
	// Retrieve protocol registries
	inboundRegistry, outboundRegistry, endpointRegistry := protocol.GetRegistries()
	return box.Context(
		context.Background(),
		inboundRegistry,
		outboundRegistry,
		endpointRegistry,
	)
}

func main() {
	var err error
	p := arg.MustParse(&args)
	switch {
	case args.Run != nil:
		err = args.Run.Run()
	case args.Check != nil:
		err = args.Check.Run()
	default:
		p.WriteHelp(os.Stderr)
	}
	if err != nil {
		if errors.Is(err, arg.ErrHelp) {
			_ = p.WriteHelpForSubcommand(os.Stderr, p.SubcommandNames()...)
		} else {
			_, _ = fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(1)
	}
}
