package humio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
)

type Q struct {
	QueryString string            `json:"queryString"`
	Start       *QueryTime        `json:"start,omitempty"`
	End         *QueryTime        `json:"end,omitempty"`
	Arguments   map[string]string `json:"arguments,omitempty"`
}

// QueryTime serializes relative or absolute times to the proper
// format for start/end times in for the humio search API. See the
// functions RelativeTime and AbsoluteTime
type QueryTime struct {
	relativeTime string
	absoluteTime time.Time
}

func (t QueryTime) MarshalJSON() ([]byte, error) {
	if t.relativeTime != "" {
		return json.Marshal(t.relativeTime)
	}

	return json.Marshal(t.absoluteTime.UnixNano() / 1e6)
}

// RelativeTime returns a QueryTime struct for specifying a relative
// start or end time such as "1minute" or "24 hours".
// See https://docs.humio.com/appendix/relative-time-syntax/
func RelativeTime(time string) *QueryTime {
	return &QueryTime{relativeTime: time}
}

// AbsoluteTime returns a QueryTime struct for specifying a absolute
// start or end time from a go time.Time struct
func AbsoluteTime(time time.Time) *QueryTime {
	return &QueryTime{absoluteTime: time}
}

// Query performs a query and returns the response body stream
// Use this for streaming responses, for smaller requests see QueryDecode
// Caller must .Close() the returned reader
func (c *Client) Query(ctx context.Context, repo string, q Q) (io.ReadCloser, error) {
	var body = &bytes.Buffer{}
	json.NewEncoder(body).Encode(q)

	span := opentracing.SpanFromContext(ctx)
	if span != nil {
		span.LogKV("query", body.String())
	}

	req, err := http.NewRequest("POST", c.GetBaseURL()+"/api/v1/repositories/"+repo+"/query", body)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Accept", "application/json") // TODO: Support ndjson too

	resp, closer, err := c.Do(ctx, req)
	defer closer()
	if err != nil {
		if span != nil {
			ext.Error.Set(span, true)
		}
		return nil, err
	}

	if err := expectStatus(ctx, resp, http.StatusOK); err != nil {
		return nil, err
	}

	return resp.Body, nil
}

// QueryJobsSync performs a query as a job and returns the response body stream
// Use this for streaming responses, for smaller requests see QueryDecode
// Caller must .Close() the returned reader
func (c *Client) QueryJobsSync(ctx context.Context, repo string, q Q) (io.ReadCloser, error) {

	var body = &bytes.Buffer{}
	json.NewEncoder(body).Encode(q)

	span := opentracing.SpanFromContext(ctx)
	if span != nil {
		span.LogKV("query", body.String())
	}

	req, err := http.NewRequest("POST", c.GetBaseURL()+"/api/v1/repositories/"+repo+"/queryjobs", body)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Accept", "application/json") // TODO: Support ndjson too
	resp, closer, err := c.Do(ctx, req)
	if err != nil {
		closer()
		return nil, err
	}

	if err := expectStatus(ctx, resp, http.StatusOK); err != nil {
		closer()
		return nil, err
	}

	var idMap map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&idMap); err != nil {
		closer()
		return nil, err
	}
	resp.Body.Close()
	closer()

	id := idMap["id"]

	defer func() {
		req, err := http.NewRequest("DELETE", c.GetBaseURL()+"/api/v1/repositories/"+repo+"/queryjobs/"+id, nil)
		if err != nil {
			// log.Println(err)
			return
		}

		resp, closer, err := c.Do(ctx, req)
		defer closer()
		if err != nil {
			// log.Println(err)
			return
		}

		if err := expectStatus(ctx, resp, http.StatusOK, http.StatusNoContent); err != nil {
			// log.Printf("query delete: %v", err)
		}

		resp.Body.Close()
	}()

	deadline, ok := ctx.Deadline()
	if ok {
		// If a deadline is given by the context, return partial results 1 second before expired
		deadline = deadline.Add(-1 * time.Second)
	} else {
		// Otherwise, set a deadline at 15 seconds
		deadline = time.Now().Add(15 * time.Second)
	}

	var partialEvents []byte

	for time.Now().Before(deadline) {
		// TODO extract function to clean up closer() + resp.Body.Close()
		req, err := http.NewRequest("GET", c.GetBaseURL()+"/api/v1/repositories/"+repo+"/queryjobs/"+id, nil)
		if err != nil {
			return nil, err
		}

		req.Header.Add("Content-Type", "application/json")
		req.Header.Add("Accept", "application/json") // TODO: Support ndjson too
		resp, closer, err := c.Do(ctx, req)
		if err != nil {
			closer()
			return nil, err
		}

		if err := expectStatus(ctx, resp, http.StatusOK); err != nil {
			closer()
			return nil, err
		}

		var status struct {
			Done     bool            `json:"done"`
			Events   json.RawMessage `json:"events"`
			Metadata struct {
				// isAggregate      bool     `json:"isAggregate"`
				PollAfter       int `json:"pollAfter"`
				ProcessedBytes  int `json:"processedBytes"`
				ProcessedEvents int `json:"processedEvents"`
				// queryEnd         int      `json:"queryEnd"`
				// queryStart       int      `json:"queryStart"`
				// resultBufferSize int      `json:"resultBufferSize"`
				// timeMillis       int      `json:"timeMillis"`
				TotalWork int `json:"totalWork"`
				//warnings         []string `json:"warnings"`
				WorkDone int `json:"workDone"`
			} `json:"metaData"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
			closer()
			return nil, err
		}
		resp.Body.Close()
		closer()

		span := opentracing.SpanFromContext(ctx)
		if span != nil {
			span.LogKV("PollAfter", status.Metadata.PollAfter,
				"ProcesedBytes", status.Metadata.ProcessedBytes,
				"ProcesedEvents", status.Metadata.ProcessedEvents,
				"TotalWork", status.Metadata.TotalWork,
				"WorkDone", status.Metadata.WorkDone,
			)
		}

		if status.Done {
			return ioutil.NopCloser(bytes.NewReader(status.Events)), nil
		}

		partialEvents = []byte(status.Events)

		pollAfter := 1000

		if status.Metadata.PollAfter >= 10 {
			pollAfter = status.Metadata.PollAfter
		}
		time.Sleep(time.Duration(pollAfter) * time.Millisecond)
	}

	// deadline close, return what we got
	if len(partialEvents) != 0 {
		return ioutil.NopCloser(bytes.NewReader(partialEvents)), nil
	}

	return nil, fmt.Errorf("timeout")
}

// QueryDecode perform a single query decodes the complete JSON
// response into "ret".  Use this for smaller responses which can be
// decoded and held in memory.  Caller must .Close() the returned
// reader. For streaming, see the Query method
func (c *Client) QueryDecode(ctx context.Context, repo string, q Q, ret interface{}) error {
	// In humio 1.8.9, "join" is not implemented by a single-request /query, only /queryjobs
	var queryFunc = c.Query
	if strings.Contains(q.QueryString, "join") {
		queryFunc = c.QueryJobsSync
	}

	resp, err := queryFunc(ctx, repo, q)
	if err != nil {
		return err
	}
	defer resp.Close()

	if err := json.NewDecoder(resp).Decode(&ret); err != nil {
		return err
	}

	return nil
}

func EscapeFieldFilter(s string) string {
	var buf bytes.Buffer
	for _, char := range s {
		switch char {
		case '"', '\\': // TODO: How to escape *?  Seems to be impossible
			buf.WriteRune('\\')
		}
		buf.WriteRune(char)
	}
	return buf.String()
}

// expectStatus returns an error with an excerpt of the payload if the
// HTTP status was not in the expected list of status codes. The body
// is closed.
func expectStatus(ctx context.Context, resp *http.Response, statusCodes ...int) error {
	for _, code := range statusCodes {
		if resp.StatusCode == code {
			return nil
		}
	}

	buf := &bytes.Buffer{}
	io.CopyN(buf, resp.Body, 1000)
	io.Copy(ioutil.Discard, resp.Body)
	resp.Body.Close()

	span := opentracing.SpanFromContext(ctx)
	if span != nil {
		ext.Error.Set(span, true)
		span.LogEvent(buf.String())
	}

	return fmt.Errorf("unexpected HTTP status %s: %s", resp.Status, buf.String())
}
