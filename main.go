package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/dominicgisler/imap-spam-cleaner/app"
	"github.com/dominicgisler/imap-spam-cleaner/inbox"
	"github.com/dominicgisler/imap-spam-cleaner/logx"
	"github.com/dominicgisler/imap-spam-cleaner/provider"
	"github.com/dominicgisler/imap-spam-cleaner/storage"
)

const appName = "imap-spam-cleaner"

var version = "dev"
var options app.Options

func init() {
	var v bool
	flag.BoolVar(&v, "version", false, "Show short version")
	flag.BoolVar(&options.RunNow, "now", false, "Run all inboxes once immediately, ignoring schedule")
	flag.BoolVar(&options.AnalyzeOnly, "analyze-only", false, "Only analyze emails, do not move or delete them")
	flag.Parse()
	if v {
		fmt.Printf("%s %s\n", appName, version)
		os.Exit(0)
	}
}

func main() {
	logx.Infof("Starting %s %s", appName, version)
	c, err := app.LoadConfig()
	if err != nil {
		logx.Errorf("Could not load config: %v", err)
		return
	}

	ctx := app.Context{
		Config:   c,
		Options:  options,
		Storages: make(map[string]*storage.Storage),
	}

	for _, inboxCfg := range c.Inboxes {
		if !inboxCfg.EnableSentWhitelist {
			continue
		}

		dbPath := storage.DBPath(inboxCfg.Host, inboxCfg.Username, inboxCfg.Inbox)
		st, err := storage.New(dbPath)
		if err != nil {
			logx.Errorf("Could not open sent contacts storage for inbox %s: %v", inboxCfg.Username, err)
			return
		}
		ctx.Storages[dbPath] = st
	}

	defer func() {
		for _, st := range ctx.Storages {
			_ = st.Close()
		}
	}()

	var p provider.Provider
	for name, prov := range c.Providers {
		p, err = provider.New(prov.Type)
		if err != nil {
			logx.Errorf("Could not load provider: %v\n", err)
			return
		}
		if err = p.ValidateConfig(prov.Config); err != nil {
			logx.Errorf("Invalid config for provider %s: %v\n", name, err)
			return
		}
		logx.Debugf("Checking provider %s (%s)", name, prov.Type)
		if err = p.HealthCheck(prov.Config); err != nil {
			logx.Errorf("Provider %s unavailable: %v\n", name, err)
			return
		}
		logx.Debugf("Provider %s (%s) health check passed", name, prov.Type)
	}

	if ctx.Options.RunNow {
		logx.Info("Running all inboxes once immediately")
		inbox.RunAllInboxes(ctx)
		return
	}

	inbox.Schedule(ctx)
}
