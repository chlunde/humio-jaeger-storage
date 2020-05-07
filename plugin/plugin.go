package plugin

import (
	"github.com/chlunde/humio-jaeger-storage/humio"
	"github.com/hashicorp/go-hclog"
)

type HumioPlugin struct {
	Logger     hclog.Logger
	Humio      *humio.Client
	Repo       string
	ReadToken  string
	WriteToken string

	spanReader       *humioSpanReader
	spanWriter       *humioSpanWriter
	dependencyReader *humioDependencyReader
}

// getClient returns a humio client with the specified token (it can
// be an API token or an ingest token)
func (h *HumioPlugin) getClient(token string) *humio.Client {
	client := *h.Humio // copy
	client.Token = token
	return &client
}
