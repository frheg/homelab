package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
)

// monitorClient reads the numbers behind the GPU card and the firing alerts.
type monitorClient struct {
	victoria, alertmanager string
	hc                     *http.Client
}

// gpuMetrics is the first GPU's state, from nvidia_gpu_exporter via VictoriaMetrics.
type gpuMetrics struct {
	MemUsedBytes  float64 `json:"mem_used_bytes"`
	MemTotalBytes float64 `json:"mem_total_bytes"`
	TempC         float64 `json:"temp_c"`
	PowerW        float64 `json:"power_w"`
	Util          float64 `json:"util"` // 0 to 1
}

type alertView struct {
	Name     string `json:"name"`
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
}

// query returns the first value of an instant query.
func (m monitorClient) query(ctx context.Context, expr string) (float64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.victoria+"/api/v1/query?query="+url.QueryEscape(expr), nil)
	if err != nil {
		return 0, err
	}
	resp, err := m.hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("victoria: %s", resp.Status)
	}
	var r struct {
		Data struct {
			Result []struct {
				Value [2]any `json:"value"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return 0, err
	}
	if len(r.Data.Result) == 0 {
		return 0, errors.New("no data for " + expr)
	}
	s, _ := r.Data.Result[0].Value[1].(string)
	return strconv.ParseFloat(s, 64)
}

func (m monitorClient) gpu(ctx context.Context) (*gpuMetrics, error) {
	var g gpuMetrics
	for _, q := range []struct {
		expr string
		dst  *float64
	}{
		{"nvidia_smi_memory_used_bytes", &g.MemUsedBytes},
		{"nvidia_smi_memory_total_bytes", &g.MemTotalBytes},
		{"nvidia_smi_temperature_gpu", &g.TempC},
		{"nvidia_smi_power_draw_watts", &g.PowerW},
		{"nvidia_smi_utilization_gpu_ratio", &g.Util},
	} {
		v, err := m.query(ctx, q.expr)
		if err != nil {
			return nil, err
		}
		*q.dst = v
	}
	return &g, nil
}

// alerts lists what is firing now: not silenced, not inhibited. Worst first.
func (m monitorClient) alerts(ctx context.Context) ([]alertView, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.alertmanager+"/api/v2/alerts?active=true&silenced=false&inhibited=false", nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("alertmanager: %s", resp.Status)
	}
	var in []struct {
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&in); err != nil {
		return nil, err
	}
	out := []alertView{}
	for _, a := range in {
		out = append(out, alertView{a.Labels["alertname"], a.Labels["severity"], a.Annotations["summary"]})
	}
	rank := func(s string) int {
		if s == "critical" {
			return 0
		}
		return 1
	}
	sort.SliceStable(out, func(i, j int) bool { return rank(out[i].Severity) < rank(out[j].Severity) })
	return out, nil
}
