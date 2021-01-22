package humio

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/humio/cli/api"
	"github.com/opentracing-contrib/go-stdlib/nethttp"
)

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

func TestToken(t *testing.T) {
	c := &Client{
		BaseURL: "http://localhost:8080",
		Token:   getIngestToken(),
		Client: &http.Client{
			Transport: &nethttp.Transport{},
			Timeout:   29 * time.Second,
		},
	}

	bi := BatchIngester{
		Period: 1 * time.Second,
		Client: c,
	}

	uniqIDForTest := fmt.Sprintf("uniq%d", time.Now().UnixNano())
	tags := map[string]string{
		"foo": "bar",
	}
	for i := 0; i < 100; i++ {
		bi.AddEvent(tags, Event{
			Timestamp: IngestTime{time.Now()},
			Attributes: map[string]string{
				"@host": "foo",
				"msg":   fmt.Sprintf("%s i-%d", uniqIDForTest, i),
			},
		})
	}

	if err := bi.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}

	// retry to allow ingest pipeline to consume data

	var events int
	for i := 0; i < 100 && events != 100; i++ {
		time.Sleep(50 * time.Millisecond)

		qj := testClient().QueryJobs()
		id, err := qj.Create("sandbox", api.Query{
			QueryString: uniqIDForTest,
		})
		if err != nil {
			panic(err)
		}

		var qr api.QueryResult
		for !qr.Done && !qr.Cancelled {
			qr, err = qj.Poll("sandbox", id)
			if err != nil {
				panic(err)
			}
		}

		events = len(qr.Events)
	}

	if events != 100 {
		t.Error("Did not found all 100 events after waiting for ingest")
	}
}
