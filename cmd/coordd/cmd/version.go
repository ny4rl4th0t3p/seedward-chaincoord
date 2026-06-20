package cmd

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// Version is set at build time via:
//
//	go build -ldflags "-X github.com/ny4rl4th0t3p/seedward-chaincoord/cmd/coordd/cmd.Version=<tag>"
var Version = "dev"

func resolvedVersion() string {
	if Version != "dev" {
		return Version
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return Version
	}
	const shortHashLen = 7
	var commit, dirty string
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if len(s.Value) > shortHashLen {
				commit = s.Value[:shortHashLen]
			} else {
				commit = s.Value
			}
		case "vcs.modified":
			if s.Value == "true" {
				dirty = "-dirty"
			}
		}
	}
	if commit == "" {
		return Version
	}
	return "dev-" + commit + dirty
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the coordd version",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Println(resolvedVersion())
		},
	}
}
