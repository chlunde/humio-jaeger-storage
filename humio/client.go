package humio

import (
	"context"
	"errors"
	"net/http"
)

const DefaultBaseURL = "https://cloud.humio.com"

// A Client is a HTTP REST client for the humio APIs.  You must at
// least initialize the Token field.  Unless BaseURL is set,
// cloud.humio.com will be used.
type Client struct {
	Client  *http.Client
	BaseURL string
	Token   string
}

func (c *Client) GetBaseURL() string {
	if c.BaseURL == "" {
		return DefaultBaseURL
	}
	return c.BaseURL
}

// Do performs the given HTTP request but sets the Authorization header
func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	if c.Token == "" {
		return nil, errors.New("client not initialized: token not set")
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)

	client := http.DefaultClient
	if c.Client != nil {
		client = c.Client
	}

	resp, err := client.Do(req.WithContext(ctx))
	// If we got an error, and the context has been canceled,
	// the context's error is probably more useful.
	if err != nil {
		select {
		case <-ctx.Done():
			err = ctx.Err()
		default:
		}
	}
	return resp, err
}
