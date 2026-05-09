package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/jonathongardner/go-starter/app"

	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v3"
)

func Run() error {
	cli.VersionPrinter = func(cmd *cli.Command) {
		fmt.Println(app.Version)
	}
	cli.VersionFlag = &cli.BoolFlag{
		Name:  "version",
		Usage: "print the version",
	}

	flags := []cli.Flag{
		&cli.BoolFlag{
			Name:    "verbose",
			Aliases: []string{"v"},
			Usage:   "logging level",
		},
	}

	app := &cli.Command{
		Name:    "bop",
		Version: app.Version,
		Usage:   "Bank of Parents — simple account ledger",
		Commands: []*cli.Command{
			serveCommand,
		},
		Flags: flags,
		Before: func(ctx context.Context, cmd *cli.Command) (context.Context, error) {
			if cmd.Bool("verbose") {
				log.SetLevel(log.DebugLevel)
				log.Debug("Setting to debug...")
			}
			return ctx, nil
		},
	}

	return app.Run(context.Background(), os.Args)
}
