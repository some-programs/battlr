package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"

	"github.com/arl/statsviz"
	"github.com/peterbourgon/ff/v3"
	"github.com/some-programs/battlr/pkg/db"
	"github.com/some-programs/battlr/pkg/scanner"
	bolt "go.etcd.io/bbolt"
)

type Flags struct {
	APIKey           string
	DB               string
	Dir              string
	Unrestricted     bool
	ShowScores       bool
	FullResultsOrder bool
	Config           string
	Listen           string
}

func (f *Flags) Register(fs *flag.FlagSet) {
	fs.StringVar(&f.APIKey, "api-key", "", "api key for administrative commands")
	fs.StringVar(&f.DB, "db", "battlr.db", "database file")
	fs.StringVar(&f.Dir, "dir", "battles/", "path to directory containing beat battles")
	fs.BoolVar(&f.Unrestricted, "unrestricted", false, "always allow voting and results")
	fs.BoolVar(&f.ShowScores, "show_scores", false, "show the score numbers in results")
	fs.BoolVar(&f.FullResultsOrder, "full_results_order", false, "show full ordered results")
	fs.StringVar(&f.Config, "config", "", "Config file")
	fs.StringVar(&f.Listen, "listen", ":8899", "http server listener")
}

func main() {

	var flags Flags

	flags.Register(flag.CommandLine)

	ff.Parse(flag.CommandLine, os.Args[1:],
		ff.WithEnvVarPrefix("BATTLR"),
		ff.WithConfigFileFlag("config"),
		ff.WithConfigFileParser(ff.PlainParser),
	)

	boltdb, err := bolt.Open(flags.DB, 0600, nil)
	if err != nil {
		panic(err)
	}
	defer boltdb.Close()

	db := &db.DB{BoltDB: boltdb}

	rootFsys := os.DirFS(flags.Dir)
	statsviz.RegisterDefault()

	fsc := scanner.FSScanner{Fsys: rootFsys}
	battles, err := scanner.GetAllBattles(fsc.GetBattleNames, fsc.GetBattle)
	if err != nil {
		slog.Error("error reading battles from directory", "dir", flags.Dir, "err", err)
		os.Exit(1)
	}
	for _, b := range battles {
		if err := db.UpdateBattle(b); err != nil {
			slog.Error("could not update battle", "err", err)
		}
	}
	server := &Server{
		DB: db,
		ServerConfig: ServerConfig{
			Unrestricted:     flags.Unrestricted,
			ShowScores:       flags.ShowScores,
			FullResultsOrder: flags.FullResultsOrder,
		},
		BattlesFsys: rootFsys,
	}
	server.RegisterHandlers(http.DefaultServeMux, flags.APIKey, rootFsys)

	srv := &http.Server{
		Addr: flags.Listen,
	}

	slog.Info("started")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("listen", "err", err)
	}
}
