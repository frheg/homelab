package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

// gpuClient is the part of the gpuctl HTTP API the panel needs.
type gpuClient struct {
	base string
	hc   *http.Client
}

func (g gpuClient) status(ctx context.Context) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.base+"/status", nil)
	if err != nil {
		return nil, err
	}
	return g.do(req)
}

func (g gpuClient) post(ctx context.Context, path string, body any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.base+path, bytes.NewReader(b))
	if err != nil {
		return err
	}
	_, err = g.do(req)
	return err
}

// do returns the body, or the error text gpuctl sent.
func (g gpuClient) do(req *http.Request) (json.RawMessage, error) {
	resp, err := g.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		var e struct{ Error string }
		json.Unmarshal(raw, &e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return nil, errors.New(e.Error)
	}
	return raw, nil
}
