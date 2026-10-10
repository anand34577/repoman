//go:build !windows

package main

import (
	"context"
	"errors"
)

// On Linux and macOS the service manager (systemd, launchd) runs `repoman serve` as an ordinary
// foreground process; unit files and install scripts are in deploy/.

func runningAsService() bool { return false }

func runService(run func(context.Context) error) error { return run(context.Background()) }

func cmdService([]string) error {
	return errors.New("`repoman service` is for Windows. On Linux use deploy/linux/install.sh (systemd), on macOS deploy/macos/install.sh (launchd); see docs/installation.md")
}
