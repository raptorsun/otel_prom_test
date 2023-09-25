package main

import (
	"fmt"

	testbed "github.com/open-telemetry/opentelemetry-collector-contrib/testbed/testbed"
)

const (
	PortPrometheus = 8080
)

func createConfigPrometheusYaml(
	sender testbed.DataSender,
	receiver testbed.DataReceiver,
	resultDir string,
	processors map[string]string,
	extensions map[string]string,
) string {
	format := `
global:
  scrape_interval: 15s # Set the scrape interval to every 15 seconds. Default is every 1 minute.
  evaluation_interval: 15s # Evaluate rules every 15 seconds. The default is every 1 minute.

scrape_configs:
  - job_name: "prometheus"
    # metrics_path defaults to '/metrics'
    # scheme defaults to 'http'.
    static_configs:
    - targets: ["localhost:%d"]
`

	// Put corresponding elements into the config template to generate the final config.
	return fmt.Sprintf(
		format,
		PortPrometheus,
	)
}

// type DataReceiver interface {
// 	Start(tc consumer.Traces, mc consumer.Metrics, lc consumer.Logs) error
// 	Stop() error

// 	// GenConfigYAMLStr generates a config string to place in exporter part of collector config
// 	// so that it can send data to this receiver.
// 	GenConfigYAMLStr() string

// 	// ProtocolName returns exporterType name to use in collector config pipeline.
// 	ProtocolName() string
// }
