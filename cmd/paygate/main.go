package main

import (
	"os"
	"runtime/debug"

	"github.com/krebs3r/paygate-cli/internal/cli"
)

// Set via -ldflags "-X main.version=… -X main.commit=… -X main.date=…".
var (
	version = ""
	commit  = ""
	date    = ""
)

func main() {
	os.Exit(cli.Execute(buildInfo()))
}

func buildInfo() cli.BuildInfo {
	b := cli.BuildInfo{Version: version, Commit: commit, Date: date}
	if info, ok := debug.ReadBuildInfo(); ok {
		if b.Version == "" && info.Main.Version != "" && info.Main.Version != "(devel)" {
			b.Version = info.Main.Version
		}
		revision, modified := "", false
		for _, s := range info.Settings {
			switch s.Key {
			case "vcs.revision":
				if len(s.Value) >= 7 {
					revision = s.Value[:7]
				}
			case "vcs.modified":
				modified = s.Value == "true"
			case "vcs.time":
				if b.Date == "" {
					b.Date = s.Value
				}
			}
		}
		if b.Commit == "" && revision != "" {
			b.Commit = revision
			if modified {
				b.Commit += "-dirty"
			}
		}
	}
	if b.Version == "" {
		b.Version = "0.1.0-dev"
	}
	if b.Commit == "" {
		b.Commit = "unknown"
	}
	if b.Date == "" {
		b.Date = "unknown"
	}
	return b
}
