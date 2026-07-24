//go:build windows

package daemon

import (
	"os"
	"os/signal"
)

func handleSignals(shutdown func()) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	<-sigCh
	shutdown()
	removeSocket()
	removePidFile()
	os.Exit(0)
}
