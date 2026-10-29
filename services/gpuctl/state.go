package main

import (
	"fmt"
	"time"
)

// State is everything gpud remembers. It is stored as one JSON document.
type State struct {
	Holder        string    `json:"holder"`         // empty: nobody holds the GPU
	Since         time.Time `json:"since"`          // when Holder became holder
	LeaseUntil    time.Time `json:"lease_until"`    // zero for the default consumer
	ReservedBy    string    `json:"reserved_by"`    // empty: not reserved
	ReservedUntil time.Time `json:"reserved_until"` // zero: until unreserve
}

func (s State) reserved(now time.Time) bool {
	return s.ReservedBy != "" && (s.ReservedUntil.IsZero() || now.Before(s.ReservedUntil))
}

// ReservedError means the GPU is reserved by a person.
type ReservedError struct {
	By    string
	Until time.Time
}

func (e *ReservedError) Error() string {
	if e.Until.IsZero() {
		return fmt.Sprintf("gpu reserved by %s until released", e.By)
	}
	return fmt.Sprintf("gpu reserved by %s until %s", e.By, e.Until.Format(time.RFC3339))
}

// BusyError means another consumer holds a lease that outlasts the wait limit.
type BusyError struct {
	Holder string
	Until  time.Time
}

func (e *BusyError) Error() string {
	return fmt.Sprintf("gpu busy: %s holds a lease until %s", e.Holder, e.Until.Format(time.RFC3339))
}

// UnknownError means no deployment carries that consumer label.
type UnknownError struct{ Name string }

func (e *UnknownError) Error() string { return "unknown consumer: " + e.Name }
