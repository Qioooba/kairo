//go:build !windows

// Package desknote presents persisted notes as native Windows desktop windows.
package desknote

import (
	"kairo/internal/note"
	"kairo/internal/winui"
)

type Controller struct{}

func New(host *winui.Host, manager *note.Manager) (*Controller, error) { return &Controller{}, nil }
func (c *Controller) NewNote() error                                   { return nil }
func (c *Controller) ToggleAll() error                                 { return nil }
func (c *Controller) Shutdown()                                        {}
