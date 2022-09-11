package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/humio/cli/api"
)

func hotrodExample() *exec.Cmd {
	cmd := exec.Command("./example-hotrod", "all", "-f", "8089")
	cmd.Env = append(cmd.Env, "JAEGER_AGENT_HOST=localhost",
		"JAEGER_AGENT_PORT=6831")

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		panic(err)
	}

	return cmd
}

func jaegerServer() *exec.Cmd {
	//cmd := exec.Command("./jaeger-all-in-one")

	cmd := exec.Command("./jaeger-all-in-one", "--grpc-storage-plugin.binary=./humio-jaeger-storage", "--grpc-storage-plugin.configuration-file=config.json", "--grpc-storage-plugin.log-level=debug")
	cmd.Env = append(cmd.Env, "SPAN_STORAGE_TYPE=grpc-plugin")

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		panic(err)
	}

	return cmd
}

func writeConfig(filename string, pc PluginConfig) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(pc); err != nil {
		panic(err)
	}

	if err := os.WriteFile("config.json", buf.Bytes(), 0644); err != nil {
		panic(err)
	}
}

func buildPlugin() {
	if out, err := exec.Command("go", "build").CombinedOutput(); err != nil {
		os.Stdout.Write(out)
		panic(err)
	}
}

func getJaeger() {
	if out, err := exec.Command("./int-test.sh").CombinedOutput(); err != nil {
		os.Stdout.Write(out)
		panic(err)
	}
}

func testClient() *api.Client {
	return api.NewClient(api.Config{
		Address: &url.URL{
			Scheme: "http",
			Host:   "localhost:8080",
		},
	})
}

func getIngestToken() string {
	client := testClient()
	tok, err := client.IngestTokens().Get("sandbox", "default")
	if err != nil {
		log.Fatalf("integration tests should run with dockerized humio on port 8080, see .github/workflows/workflow.yaml. docker run -p 8080:8080 --rm docker.io/humio/humio:1.18.4: %v", err)
	}

	return tok.Token
}

func TestIntegration(t *testing.T) {
	getJaeger()
	buildPlugin()

	pc := PluginConfig{
		Humio:      "http://localhost:8080",
		Repo:       "sandbox",
		ReadToken:  "",
		WriteToken: getIngestToken(),
	}

	writeConfig("config.json", pc)
	jaeger := jaegerServer()
	hotrod := hotrodExample()

	time.Sleep(1 * time.Second)

	var testIDs []string
	for i := 0; i < 10; i++ {
		testID := fmt.Sprintf("%dtestID%d", i, time.Now().UnixNano())
		testIDs = append(testIDs, testID)
		url := fmt.Sprintf("http://localhost:8089/dispatch?customer=123&foo=%s", testID)
		resp, err := http.Get(url)
		if err != nil {
			panic(err)
		}
		if resp.StatusCode != http.StatusOK {
			panic("status code")
		}
	}

	time.Sleep(6 * time.Second)

	deadline, ok := t.Deadline()
	minute := time.Now().Add(1 * time.Minute)
	if !ok || deadline.After(minute) {
		deadline = minute
	}
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	for _, testID := range testIDs {
		end := fmt.Sprintf("%d", time.Now().UnixNano()/1000)
		start := fmt.Sprintf("%d", time.Now().Add(-2*time.Minute).UnixNano()/1000)
		req, err := http.NewRequestWithContext(ctx, "GET", "http://localhost.localdomain:16686/api/traces?end="+end+"&limit=20&lookback=1h&maxDuration&minDuration&service=frontend&start="+start+"&tags=%7B%22http.url%22%3A%22%2A"+testID+"%2A%22%7D", nil)
		if err != nil {
			panic(err)
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			panic(err)
		}

		type UIResp struct {
			Data []struct {
				TraceID string        `json:"traceID"`
				Spans   []interface{} `json:"spans"`
			} `json:"data"`
		}

		var body bytes.Buffer
		var uiResp UIResp
		if err := json.NewDecoder(io.TeeReader(resp.Body, &body)).Decode(&uiResp); err != nil {
			panic(err)
		}

		if len(uiResp.Data) != 1 {
			t.Log(body.String())
			t.Fatalf("Expected 1 trace, got %d", len(uiResp.Data))
		}

		if len(uiResp.Data[0].Spans) < 50 {
			t.Errorf("Expected 50+ spans for trace")
		}
	}

	if t.Failed() {
		jaeger.Process.Signal(syscall.SIGABRT)
		time.Sleep(1 * time.Second)
	}

	jaeger.Process.Kill()
	hotrod.Process.Kill()

	if err := jaeger.Wait(); err != nil {
		if !strings.Contains(err.Error(), "signal: killed") {
			t.Fatal(err)
		}
	}

	if err := hotrod.Wait(); err != nil {
		if !strings.Contains(err.Error(), "signal: killed") {
			t.Fatal(err)
		}
	}
}
