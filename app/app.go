package app

import "runtime/debug"

var Version string

func init() {
	Version = "unknown"
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Sum != "" {
		Version = info.Main.Version
	}
}
