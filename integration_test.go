package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/humio/cli/api"
)

func hotrodExample() *exec.Cmd {
	cmd := exec.Command("./example-hotrod", "all", "-f", "8089")
	cmd.Env = append(cmd.Env, "JAEGER_AGENT_HOST=localhost",
		"JAEGER_AGENT_PORT=6831")

	cmd.Stdout = ioutil.Discard
	cmd.Stderr = ioutil.Discard
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

	if err := ioutil.WriteFile("config.json", buf.Bytes(), 0644); err != nil {
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
		panic(err)
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

	time.Sleep(2 * time.Second)

	for _, testID := range testIDs {
		end := fmt.Sprintf("%d", time.Now().UnixNano()/1000)
		start := fmt.Sprintf("%d", time.Now().Add(-2*time.Minute).UnixNano()/1000)
		resp, err := http.Get("http://localhost.localdomain:16686/api/traces?end=" + end + "&limit=20&lookback=1h&maxDuration&minDuration&service=frontend&start=" + start + "&tags=%7B%22http.url%22%3A%22%2A" + testID + "%2A%22%7D")
		// resp, err := http.Get("http://localhost.localdomain:16686/api/traces?end=" + end + "&limit=20&lookback=1h&maxDuration&minDuration&service=frontend&start=" + start + "&tags=%7B%22http.url%22%3A%22%2Fdispatch%3Fcustomer%3D123%26foo%3D" + testID + "%22%7D")
		if err != nil {
			panic(err)
		}

		type UIResp struct {
			Data []struct {
				TraceID string        `json:"traceID"`
				Spans   []interface{} `json:"spans"`
			} `json:"data"`
		}

		var uiResp UIResp
		if err := json.NewDecoder(resp.Body).Decode(&uiResp); err != nil {
			panic(err)
		}

		if len(uiResp.Data) != 1 {
			t.Fatalf("Expected 1 trace, got %d", len(uiResp.Data))
		}

		if len(uiResp.Data[0].Spans) < 50 {
			t.Errorf("Expected 50+ spans for trace")
		}
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
