//go:build headless

package main

// Headless builds leave Ebiten out entirely: its package init talks to the
// window station, which panics in services and non-interactive SSH sessions.

import (
	"context"
	"errors"
)

func runUI(ctx context.Context, relay *Relay, o options) error {
	return errors.New("this build has no window support; run without --headless using the GUI binary")
}
