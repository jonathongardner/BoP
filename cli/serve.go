package cli

import (
	"context"
	"net/http"
	"os"
	"path/filepath"

	"github.com/jonathongardner/go-starter/internal/server"
	"github.com/jonathongardner/go-starter/internal/store"
	"github.com/jonathongardner/go-starter/web"

	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v3"
)

var serveCommand = &cli.Command{
	Name:  "serve",
	Usage: "Run the BoP web UI and API",
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:    "listen",
			Aliases: []string{"l"},
			Value:   "127.0.0.1:8080",
			Usage:   "HTTP listen address",
		},
		&cli.StringFlag{
			Name:    "db",
			Value:   "bop.db",
			Usage:   "SQLite database file path",
		},
	},
	Action: func(ctx context.Context, cmd *cli.Command) error {
		dbPath := cmd.String("db")
		if dir := filepath.Dir(dbPath); dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return err
			}
		}

		st, err := store.Open(dbPath)
		if err != nil {
			return err
		}
		defer st.Close()

		addr := cmd.String("listen")
		srv := server.New(st, web.IndexHTML)
		log.Infof("Listening on http://%s (database %s)", addr, dbPath)

		httpServer := &http.Server{
			Addr:    addr,
			Handler: srv.Handler(),
		}

		go func() {
			<-ctx.Done()
			_ = httpServer.Shutdown(context.Background())
		}()

		err = httpServer.ListenAndServe()
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	},
}
