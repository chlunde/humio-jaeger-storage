package humio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"time"

	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
)

type Event struct {
	Timestamp  IngestTime        `json:"timestamp"`
	Attributes map[string]string `json:"attributes"`
}

type IngestTime struct {
	time.Time
}

func (t IngestTime) MarshalJSON() ([]byte, error) {
	rfcTime := t.Time.Format(time.RFC3339)
	return []byte(fmt.Sprintf(`"%s"`, rfcTime)), nil
}

type eventStream struct {
	Tags   map[string]string `json:"tags"`
	Events []Event           `json:"events"`
}

type BatchIngester struct {
	Period time.Duration
	Client *Client

	buffer []eventStream
	mu     sync.Mutex
}

func (b *BatchIngester) AddEvent(tags map[string]string, e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	var found = false
	var es *eventStream
	for i, e := range b.buffer {
		if reflect.DeepEqual(e.Tags, tags) {
			es = &b.buffer[i]
			found = true
			break
		}
	}

	if !found {
		b.buffer = append(b.buffer, eventStream{
			Tags: tags,
		})
		es = &b.buffer[len(b.buffer)-1]
	}

	es.Events = append(es.Events, e)
}

/*
[
  {
    "tags": {
      "host": "server1",
      "source": "application.log"
    },
    "events": [
      {
        "timestamp": "2016-06-06T12:00:00+02:00",
        "attributes": {
          "key1": "value1",
          "key2": "value2"
        }
      },
      {
        "timestamp": "2016-06-06T12:00:01+02:00",
        "attributes": {
          "key1": "value1"
        }
      }
    ]
  }
]
*/

func (i *BatchIngester) Flush(ctx context.Context) error {
	i.mu.Lock()
	defer i.mu.Unlock()

	//log.Printf("Sending %d event streams", len(i.buffer))
	if len(i.buffer) == 0 {
		return nil
	}

	span, ctx := opentracing.StartSpanFromContext(ctx, "Flush")
	defer span.Finish()

	// POST /api/v1/ingest/humio-structured
	var body = &bytes.Buffer{}
	if err := json.NewEncoder(body).Encode(i.buffer); err != nil {
		i.buffer = nil // should not be possible, maybe panic instead?
		return err
	}

	span.LogKV("spans", len(i.buffer), "bytes_encoded", body.Len())

	req, err := http.NewRequest("POST", i.Client.GetBaseURL()+"/api/v1/ingest/humio-structured", body)
	if err != nil {
		// log.Printf("JSON: %s", body.String())
		return err
	}

	resp, err := i.Client.Do(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if err := expectStatus(resp, http.StatusOK); err != nil {
		ext.Error.Set(span, true)

		// Flush buffer on some errors to avoid corrupt data
		// stopping this node and eating all memory, like Bad Request

		if resp.StatusCode < 500 { // i.e. http.StatusBadRequest
			i.buffer = nil
		}

		return err
	}

	i.buffer = nil

	return nil
}
