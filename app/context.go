package app

import "github.com/dominicgisler/imap-spam-cleaner/storage"

type Options struct {
	RunNow      bool
	AnalyzeOnly bool
}

type Context struct {
	Config   *Config
	Options  Options
	Storages map[string]*storage.Storage
}
