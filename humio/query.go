package humio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"time"
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

// RelativeTime returns a QueryTime struct for specifying a absolute
// start or end time from a go time.Time struct
func AbsoluteTime(time time.Time) *QueryTime {
	return &QueryTime{absoluteTime: time}
}

// Perform a query and response response body stream
// Use this for streaming responses, for smaller requests see QueryDecode
// Caller must .Close() the returned reader
func (c *Client) Query(ctx context.Context, repo string, q Q) (io.ReadCloser, error) {
	var body = &bytes.Buffer{}
	json.NewEncoder(body).Encode(q)

	log.Printf("JSON: %s", body.String())
	req, err := http.NewRequest("POST", c.GetBaseURL()+"/api/v1/repositories/"+repo+"/query", body)
	if err != nil {
		return nil, err
	}

	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Accept", "application/json") // TODO: Support ndjson too
	resp, err := c.Do(ctx, req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		buf := &bytes.Buffer{}
		io.CopyN(buf, resp.Body, 1000)
		io.Copy(ioutil.Discard, resp.Body)
		return nil, fmt.Errorf("query: unexpected HTTP status %s: %s", resp.Status, buf.String())
	}

	return resp.Body, nil
}

// Perform a query and decode JSON into "ret".
// Use this for smaller responses which can be decoded and held in memory.
// Caller must .Close() the returned reader
func (c *Client) QueryDecode(ctx context.Context, repo string, q Q, ret interface{}) error {
	resp, err := c.Query(ctx, repo, q)
	if err != nil {
		return err
	}
	defer resp.Close()

	//if err := json.NewDecoder(io.TeeReader(resp, os.Stdout)).Decode(&ret); err != nil {
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
