package main

import "context"

// Store keeps State across restarts.
type Store interface {
	Load(ctx context.Context) (State, error)
	Save(ctx context.Context, s State) error
}
