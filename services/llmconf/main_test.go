package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	p := filepath.Join(dir, "models")
	os.MkdirAll(p, 0o755)
	if err := os.WriteFile(filepath.Join(p, name+".yaml"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const gpuModel = "runtime: gpu\ndescription: d\nttl: 600\ncmd: >-\n  /app/llama-server --port 9999\n  -m /models/a.gguf -ngl 999\n"
const cpuModel = "runtime: cpu\ndescription: d\nttl: 600\ncmd: >-\n  /app/llama-server --port 9999\n  -m /models/b.gguf -ngl 0 -t 8\n"

func TestModelsAreSplitByRuntime(t *testing.T) {
	d := t.TempDir()
	write(t, d, "big", gpuModel)
	write(t, d, "small", cpuModel)
	out, bad, err := build(d)
	if err != nil || len(bad) > 0 {
		t.Fatalf("err=%v bad=%v", err, bad)
	}
	if !strings.Contains(string(out["config-gpu.yaml"]), "big:") || strings.Contains(string(out["config-gpu.yaml"]), "small:") {
		t.Errorf("gpu config wrong:\n%s", out["config-gpu.yaml"])
	}
	if !strings.Contains(string(out["config-cpu.yaml"]), "small:") || strings.Contains(string(out["config-cpu.yaml"]), "big:") {
		t.Errorf("cpu config wrong:\n%s", out["config-cpu.yaml"])
	}
	if !strings.Contains(string(out["config-gpu.yaml"]), "cmd: /app/llama-server --port 9999 -m /models/a.gguf -ngl 999") {
		t.Errorf("cmd not folded to one line:\n%s", out["config-gpu.yaml"])
	}
}

func TestCPUModelWithoutNglZeroIsRejected(t *testing.T) {
	d := t.TempDir()
	write(t, d, "oops", strings.Replace(cpuModel, "-ngl 0 -t 8", "-ngl 999", 1))
	_, bad, _ := build(d)
	if len(bad) == 0 || !strings.Contains(bad[0], "-ngl 0") {
		t.Fatalf("bad = %v", bad)
	}
}

func TestOtherMistakesAreRejected(t *testing.T) {
	cases := map[string]string{
		"runtime": strings.Replace(gpuModel, "runtime: gpu", "runtime: tpu", 1),
		"ttl":     strings.Replace(gpuModel, "ttl: 600", "ttl: 0", 1),
		"port":    strings.Replace(gpuModel, "--port 9999", "--port 8080", 1),
		"model":   strings.Replace(gpuModel, "-m /models/a.gguf", "-m /tmp/a.gguf", 1),
		"unknown": gpuModel + "colour: red\n",
	}
	for name, body := range cases {
		d := t.TempDir()
		write(t, d, "m", body)
		_, bad, _ := build(d)
		if len(bad) == 0 {
			t.Errorf("%s: mistake not caught", name)
		}
	}
}

func TestNoModelsIsAnError(t *testing.T) {
	if _, _, err := build(t.TempDir()); err == nil {
		t.Fatal("empty directory must fail, a check that sees nothing must not pass")
	}
}

func TestOutputIsStable(t *testing.T) {
	d := t.TempDir()
	write(t, d, "a", gpuModel)
	write(t, d, "b", gpuModel)
	x, _, _ := build(d)
	y, _, _ := build(d)
	if string(x["config-gpu.yaml"]) != string(y["config-gpu.yaml"]) {
		t.Fatal("generator output changes between runs")
	}
}

func TestConfidentialModelsGetAGatewayThatKeepsNothing(t *testing.T) {
	d := t.TempDir()
	write(t, d, "secret", gpuModel+"confidential: true\n")
	write(t, d, "open", strings.Replace(gpuModel, "a.gguf", "c.gguf", 1))
	out, bad, err := build(d)
	if err != nil || len(bad) > 0 {
		t.Fatalf("err=%v bad=%v", err, bad)
	}
	c := string(out["config-confidential.yaml"])
	if !strings.Contains(c, "secret:") || strings.Contains(c, "open:") {
		t.Errorf("confidential config has the wrong models:\n%s", c)
	}
	for _, want := range []string{"captureBuffer: 0", "logToStdout: none", "logLevel: warn"} {
		if !strings.Contains(c, want) {
			t.Errorf("confidential config lacks %q:\n%s", want, c)
		}
	}
	if !strings.Contains(string(out["config-gpu.yaml"]), "secret:") {
		t.Errorf("a confidential model is still served by the normal gateway")
	}
	d2 := t.TempDir()
	write(t, d2, "x", cpuModel+"confidential: true\n")
	if _, bad, _ := build(d2); len(bad) == 0 || !strings.Contains(bad[0], "runtime must be gpu") {
		t.Fatalf("confidential cpu model passed: %v", bad)
	}
}
