//go:build !windows

package daemon

import (
	"os"
	"os/signal"
	"syscall"
)

func handleSignals(shutdown func()) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)
	<-sigCh
	shutdown()
	removeSocket()
	removePidFile()
	os.Exit(0)
}
