package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"
)

const (
	defaultTTL = 10 * time.Minute
	maxTTL     = 24 * time.Hour
)

type request struct {
	Name string `json:"name"`
	TTL  string `json:"ttl"`
	By   string `json:"by"`
	For  string `json:"for"`
}

func newMux(a *Arbiter) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		reply(w, a.Status(), nil)
	})
	mux.HandleFunc("POST /acquire", func(w http.ResponseWriter, r *http.Request) {
		req, ok := decode(w, r)
		if !ok {
			return
		}
		ttl, err := duration(req.TTL, defaultTTL)
		if err != nil || ttl <= 0 || ttl > maxTTL {
			reply(w, nil, badRequest("ttl must be between 1s and 24h"))
			return
		}
		s, err := a.Acquire(r.Context(), req.Name, ttl)
		reply(w, s, err)
	})
	mux.HandleFunc("POST /release", func(w http.ResponseWriter, r *http.Request) {
		req, ok := decode(w, r)
		if !ok {
			return
		}
		err := a.Release(r.Context(), req.Name)
		reply(w, a.Status(), err)
	})
	mux.HandleFunc("POST /reserve", func(w http.ResponseWriter, r *http.Request) {
		req, ok := decode(w, r)
		if !ok {
			return
		}
		d, err := duration(req.For, 0)
		if err != nil || d < 0 || req.By == "" {
			reply(w, nil, badRequest("need by, and for as a duration"))
			return
		}
		err = a.Reserve(r.Context(), req.By, d)
		reply(w, a.Status(), err)
	})
	mux.HandleFunc("POST /unreserve", func(w http.ResponseWriter, r *http.Request) {
		err := a.Unreserve(r.Context())
		reply(w, a.Status(), err)
	})
	return mux
}

type badRequest string

func (e badRequest) Error() string { return string(e) }

func decode(w http.ResponseWriter, r *http.Request) (request, bool) {
	var req request
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		reply(w, nil, badRequest("bad json"))
		return req, false
	}
	return req, true
}

func duration(s string, def time.Duration) (time.Duration, error) {
	if s == "" {
		return def, nil
	}
	return time.ParseDuration(s)
}

// reply writes the state, or the error with a status the CLI maps to an exit code.
func reply(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		var (
			res  *ReservedError
			busy *BusyError
			unk  *UnknownError
			bad  badRequest
		)
		switch {
		case errors.As(err, &res):
			w.WriteHeader(http.StatusLocked)
		case errors.As(err, &busy):
			w.WriteHeader(http.StatusConflict)
		case errors.As(err, &unk):
			w.WriteHeader(http.StatusNotFound)
		case errors.As(err, &bad):
			w.WriteHeader(http.StatusBadRequest)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(v)
}
