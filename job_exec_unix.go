//go:build !windows

package main

import (
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/term"
)

func watchTerminalSize(fd int) (<-chan [2]int, func()) {
	sizes := make(chan [2]int, 1)
	signals := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(signals, syscall.SIGWINCH)
	go func() {
		for {
			select {
			case <-signals:
				cols, rows, err := term.GetSize(fd)
				if err == nil {
					select {
					case sizes <- [2]int{rows, cols}:
					default:
					}
				}
			case <-done:
				return
			}
		}
	}()
	return sizes, func() { signal.Stop(signals); close(done) }
}
