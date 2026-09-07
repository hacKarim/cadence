// Package version carries build metadata stamped in at link time.
package version

import "runtime/debug"

// Values are overridden with -ldflags at release time.
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

// String renders a one-line description of this build.
func String() string {
	s := "cadence " + Version
	if Commit == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, kv := range info.Settings {
				if kv.Key == "vcs.revision" && len(kv.Value) >= 7 {
					Commit = kv.Value[:7]
				}
			}
		}
	}
	if Commit != "" {
		s += " (" + Commit + ")"
	}
	if Date != "" {
		s += " built " + Date
	}
	return s
}
