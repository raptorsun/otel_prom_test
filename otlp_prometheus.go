package main

import (
	"fmt"
	"log"
	"os"
	"path"
	"path/filepath"
	"time"

	testbed "github.com/open-telemetry/opentelemetry-collector-contrib/testbed/testbed"
	// datasenders "github.com/open-telemetry/opentelemetry-collector-contrib/testbed/datasenders"
)

const (
	AppName              = "otlp_prometheus"
	AddressLocalhost     = "127.0.0.1"
	PortReceiverHTTP     = 34687
	PortExporterHTTP     = 34688
	ExePathOtelCollector = "/home/hsun/opentelemetry-collector-contrib/bin/otelcontribcol_linux_amd64"
	ExePathPrometheus    = "/home/hsun/prometheus/prometheus"
)

func main() {
	log.SetOutput(os.Stdout)
	log.SetFlags(log.Ldate | log.Ltime)
	log.SetPrefix("otlp_prom: ")
	log.Println(AppName)

	sendToPrometheus()
}

func sendToPrometheus() {

	sender := testbed.NewOTLPHTTPMetricDataSender(AddressLocalhost, PortReceiverHTTP)
	receiver := testbed.NewOTLPHTTPDataReceiver(PortExporterHTTP)

	resourceSpec := testbed.ResourceSpec{
		ExpectedMaxCPU:      1200,
		ExpectedMaxRAM:      5500,
		ResourceCheckPeriod: 3 * time.Second,
	}

	resultDir, err := filepath.Abs(path.Join("results", AppName))
	if err != nil {
		log.Fatalf(err.Error())
	}

	agentProc := testbed.NewChildProcessCollector(testbed.WithAgentExePath(ExePathOtelCollector))

	// mock backend only
	// configStr := createConfigYaml(sender, receiver, resultDir, nil, nil)
	// remote write
	// configStr := createConfigOtelRemoteWriteYaml(sender, receiver, resultDir, nil, nil)
	configStr := createConfigOtelNativeeYaml(sender, receiver, resultDir, nil, nil)
	log.Printf("Otel Config: %s", configStr)
	configCleanupOtel, err := agentProc.PrepareConfig(configStr)
	if err != nil {
		log.Fatalf(err.Error())
		return
	}

	defer configCleanupOtel()

	promRunner := NewPrometheusRunner(WithAgentExePath(ExePathPrometheus))
	configStrProm := createConfigPrometheusYaml()
	log.Printf("Prom Config: %s", configStrProm)
	configCleanUpProm, err := promRunner.PrepareConfig(configStrProm)
	if err != nil {
		log.Fatalf(err.Error())
		return
	}

	defer configCleanUpProm()

	options := testbed.LoadOptions{
		DataItemsPerSecond: 10_000,
		ItemsPerBatch:      100,
		Parallel:           1,
	}
	dataProvider := testbed.NewPerfTestDataProvider(options)
	log.Println("DataProvider created", dataProvider)

	resultsSummary := &testbed.PerformanceResults{}
	scenario := NewScenario(
		AppName,
		dataProvider,
		sender,
		receiver,
		agentProc,
		promRunner,
		&testbed.PerfTestValidator{},
		resultsSummary,
		resourceSpec,
	)

	defer scenario.Stop()

	scenario.StartBackend()
	scenario.StartAgent()
	scenario.StartPrometheus(fmt.Sprintf("--web.listen-address=:%d", PortPrometheus),
		"--enable-feature=otlp-write-receiver",
		"--web.enable-remote-write-receiver")

	scenario.StartLoad(options)

	scenario.Sleep(scenario.Duration)

	scenario.StopLoad()

	scenario.WaitForN(func() bool { return scenario.LoadGenerator.DataItemsSent() > 0 }, 10*time.Second, "load generator started")
	scenario.WaitForN(func() bool { return scenario.LoadGenerator.DataItemsSent() == scenario.MockBackend.DataItemsReceived() }, 10*time.Second,
		"all data items received")

	scenario.StopAgent()
	scenario.StopPrometheus()
	tenMetrics := scenario.MockBackend.ReceivedMetrics[0:3]
	for _, metric := range tenMetrics {
		rm := metric.ResourceMetrics()

		log.Printf("metric: %v", rm.At(0).Resource().Attributes().AsRaw())
	}

	// tc.ValidateData()
}

func createConfigYaml(
	sender testbed.DataSender,
	receiver testbed.DataReceiver,
	resultDir string,
	processors map[string]string,
	extensions map[string]string,
) string {

	// Create a config. Note that our DataSender is used to generate a config for Collector's
	// receiver and our DataReceiver is used to generate a config for Collector's exporter.
	// This is because our DataSender sends to Collector's receiver and our DataReceiver
	// receives from Collector's exporter.

	// Prepare extra processor config section and comma-separated list of extra processor
	// names to use in corresponding "processors" settings.
	processorsSections := ""
	processorsList := ""
	if len(processors) > 0 {
		first := true
		for name, cfg := range processors {
			processorsSections += cfg + "\n"
			if !first {
				processorsList += ","
			}
			processorsList += name
			first = false
		}
	}

	// Prepare extra extension config section and comma-separated list of extra extension
	// names to use in corresponding "extensions" settings.
	extensionsSections := ""
	extensionsList := ""
	if len(extensions) > 0 {
		first := true
		for name, cfg := range extensions {
			extensionsSections += cfg + "\n"
			if !first {
				extensionsList += ","
			}
			extensionsList += name
			first = false
		}
	}

	// Set pipeline based on DataSender type
	var pipeline string
	switch sender.(type) {
	case testbed.TraceDataSender:
		pipeline = "traces"
	case testbed.MetricDataSender:
		pipeline = "metrics"
	case testbed.LogDataSender:
		pipeline = "logs"
	default:
		log.Fatalf("Invalid DataSender type")
		return ""
	}

	format := `
receivers:%v
exporters:%v
processors:
  %s

extensions:
  pprof:
    save_to_file: %v/cpu.prof
  %s

service:
  extensions: [pprof, %s]
  pipelines:
    %s:
      receivers: [%v]
      processors: [%s]
      exporters: [%v]
`

	// Put corresponding elements into the config template to generate the final config.
	return fmt.Sprintf(
		format,
		sender.GenConfigYAMLStr(),
		receiver.GenConfigYAMLStr(),
		processorsSections,
		resultDir,
		extensionsSections,
		extensionsList,
		pipeline,
		sender.ProtocolName(),
		processorsList,
		receiver.ProtocolName(),
	)
}

func createConfigOtelRemoteWriteYaml(
	sender testbed.DataSender,
	receiver testbed.DataReceiver,
	resultDir string,
	processors map[string]string,
	extensions map[string]string,
) string {

	// Create a config. Note that our DataSender is used to generate a config for Collector's
	// receiver and our DataReceiver is used to generate a config for Collector's exporter.
	// This is because our DataSender sends to Collector's receiver and our DataReceiver
	// receives from Collector's exporter.

	// Prepare extra processor config section and comma-separated list of extra processor
	// names to use in corresponding "processors" settings.
	processorsSections := ""
	processorsList := ""
	if len(processors) > 0 {
		first := true
		for name, cfg := range processors {
			processorsSections += cfg + "\n"
			if !first {
				processorsList += ","
			}
			processorsList += name
			first = false
		}
	}

	// Prepare extra extension config section and comma-separated list of extra extension
	// names to use in corresponding "extensions" settings.
	extensionsSections := ""
	extensionsList := ""
	if len(extensions) > 0 {
		first := true
		for name, cfg := range extensions {
			extensionsSections += cfg + "\n"
			if !first {
				extensionsList += ","
			}
			extensionsList += name
			first = false
		}
	}

	// Set pipeline based on DataSender type
	var pipeline string
	switch sender.(type) {
	case testbed.TraceDataSender:
		pipeline = "traces"
	case testbed.MetricDataSender:
		pipeline = "metrics"
	case testbed.LogDataSender:
		pipeline = "logs"
	default:
		log.Fatalf("Invalid DataSender type")
		return ""
	}

	format := `
receivers:%v
exporters:%v
processors:
  %s

extensions:
  pprof:
    save_to_file: %v/cpu.prof
  %s

service:
  extensions: [pprof, %s]
  pipelines:
    %s:
      receivers: [%v]
      processors: [%s]
      exporters: [%v]
`

	remoteWriteYAMLStr := `
  prometheusremotewrite:
    endpoint: "http://localhost:8080/api/v1/write"
    external_labels:
      scenario: otlp_prometheus_remote_write
    export_created_metric:
      enabled: true
`
	loggingYAMLStr := `
  logging:


`
	// Put corresponding elements into the config template to generate the final config.
	return fmt.Sprintf(
		format,
		sender.GenConfigYAMLStr(),
		receiver.GenConfigYAMLStr()+remoteWriteYAMLStr+loggingYAMLStr,
		processorsSections,
		resultDir,
		extensionsSections,
		extensionsList,
		pipeline,
		sender.ProtocolName(),
		processorsList,
		receiver.ProtocolName()+",prometheusremotewrite"+",logging",
	)
}

func createConfigOtelNativeeYaml(
	sender testbed.DataSender,
	receiver testbed.DataReceiver,
	resultDir string,
	processors map[string]string,
	extensions map[string]string,
) string {

	// Create a config. Note that our DataSender is used to generate a config for Collector's
	// receiver and our DataReceiver is used to generate a config for Collector's exporter.
	// This is because our DataSender sends to Collector's receiver and our DataReceiver
	// receives from Collector's exporter.

	// Prepare extra processor config section and comma-separated list of extra processor
	// names to use in corresponding "processors" settings.
	processorsSections := ""
	processorsList := ""
	if len(processors) > 0 {
		first := true
		for name, cfg := range processors {
			processorsSections += cfg + "\n"
			if !first {
				processorsList += ","
			}
			processorsList += name
			first = false
		}
	}

	// Prepare extra extension config section and comma-separated list of extra extension
	// names to use in corresponding "extensions" settings.
	extensionsSections := ""
	extensionsList := ""
	if len(extensions) > 0 {
		first := true
		for name, cfg := range extensions {
			extensionsSections += cfg + "\n"
			if !first {
				extensionsList += ","
			}
			extensionsList += name
			first = false
		}
	}

	// Set pipeline based on DataSender type
	var pipeline string
	switch sender.(type) {
	case testbed.TraceDataSender:
		pipeline = "traces"
	case testbed.MetricDataSender:
		pipeline = "metrics"
	case testbed.LogDataSender:
		pipeline = "logs"
	default:
		log.Fatalf("Invalid DataSender type")
		return ""
	}

	format := `
receivers:%v
exporters:%v
processors:
  %s

extensions:
  pprof:
    save_to_file: %v/cpu.prof
  %s

service:
  extensions: [pprof, %s]
  pipelines:
    %s:
      receivers: [%v]
      processors: [%s]
      exporters: [%v]
`

	otlpNativeYAMLStr := `
  otlphttp/prometheus:
    endpoint: "http://localhost:8080/api/v1/otlp"
    tls:
      insecure: true
`
	loggingYAMLStr := `
  logging:


`
	// Put corresponding elements into the config template to generate the final config.
	return fmt.Sprintf(
		format,
		sender.GenConfigYAMLStr(),
		receiver.GenConfigYAMLStr()+otlpNativeYAMLStr+loggingYAMLStr,
		processorsSections,
		resultDir,
		extensionsSections,
		extensionsList,
		pipeline,
		sender.ProtocolName(),
		processorsList,
		receiver.ProtocolName()+",otlphttp/prometheus"+",logging",
	)
}
