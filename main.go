package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"time"

	"github.com/chlunde/humio-jaeger-storage/humio"
	"github.com/chlunde/humio-jaeger-storage/plugin"
	"github.com/hashicorp/go-hclog"
	"github.com/opentracing-contrib/go-stdlib/nethttp"
	"github.com/opentracing/opentracing-go"
	"github.com/uber/jaeger-client-go/config"

	"github.com/jaegertracing/jaeger/plugin/storage/grpc"
)

// PluginConfig is the file format for our config
// ./plugin -config configuration.json
type PluginConfig struct {
	ReadToken  string `json:"readToken"`
	WriteToken string `json:"writeToken"`
	Repo       string `json:"repo"`
	Humio      string `json:"humio"`
}

const serviceName = "humio-jaeger-storage"

func main() {
	// Parse command line options
	var configPath string
	flag.StringVar(&configPath, "config", "", "A path to the plugin's configuration file")
	flag.Parse()

	os.Setenv("JAEGER_REPORTER_FLUSH_INTERVAL", "1s")
	os.Setenv("JAEGER_SAMPLER_TYPE", "const")
	os.Setenv("JAEGER_SAMPLER_PARAM", "1")
	/*
		os.Setenv("JAEGER_REPORTER_LOG_SPANS", "true")
	*/

	// Set up logger. Our stdout/stderr is not propagated to the user,
	// so be sure to use this for important messages
	logger := hclog.New(&hclog.LoggerOptions{
		Name:       serviceName,
		Level:      hclog.Warn,
		JSONFormat: true,
	})

	// Attempt to set up jaeger to trace ourself
	conf, err := config.FromEnv()
	if err != nil {
		logger.Warn("Jaeger client config failed", "err", err)
		conf = &config.Configuration{}
	}

	conf.ServiceName = serviceName
	tracer, closer, err := conf.NewTracer(config.Logger(&jaegerTohcLog{inner: logger}))
	if err != nil {
		logger.Error("Failed to configure jaeger client", "err", err)
		os.Exit(1)
	}

	defer closer.Close()
	opentracing.SetGlobalTracer(tracer)

	// Parse plugin config with tokens etc.
	logger.Warn("Config path", "path", configPath)
	config, err := readConfig(configPath)
	if err != nil {
		logger.Error("Reading config failed", "err", err.Error())
		os.Exit(1)
	}

	plugin := plugin.HumioPlugin{
		Logger:     logger,
		Repo:       config.Repo,
		ReadToken:  config.ReadToken,
		WriteToken: config.WriteToken,
		Humio: &humio.Client{
			BaseURL: config.Humio,
			Client: &http.Client{
				Transport: &nethttp.Transport{},
				Timeout:   29 * time.Second},
		},
	}

	grpc.Serve(&plugin)
}

func readConfig(path string) (*PluginConfig, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var pluginConfig PluginConfig
	return &pluginConfig, json.Unmarshal(data, &pluginConfig)
}

// jaegerTohclog makes a hashicorp logger implement the jaeger logger interface
type jaegerTohcLog struct {
	inner hclog.Logger
}

// Error logs a message at error priority
func (j jaegerTohcLog) Error(msg string) {
	j.inner.Error(msg)
}

// Infof logs a message at info priority
func (j jaegerTohcLog) Infof(msg string, args ...interface{}) {
	j.inner.Warn("INFO " + fmt.Sprintf(msg, args...))
}
