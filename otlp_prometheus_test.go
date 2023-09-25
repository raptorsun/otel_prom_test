package main

import (
	testbed "github.com/open-telemetry/opentelemetry-collector-contrib/testbed/testbed"
	testbed_tests "github.com/open-telemetry/opentelemetry-collector-contrib/testbed/tests"

	"testing"
)

func TestMetric10kDPS(t *testing.T) {
	performanceResultsSummary := &testbed.PerformanceResults{}

	// testbed.GlobalConfig.DefaultAgentExeRelativeFile = "/home/hsun/opentelemetry-collector-contrib/bin/oteltestbedcol_linux_amd64"
	testbed.GlobalConfig.DefaultAgentExeRelativeFile = "/home/hsun/opentelemetry-collector-contrib/bin/otelcontribcol_linux_amd64"

	name := "OTLP-HTTP"
	sender := testbed.NewOTLPHTTPMetricDataSender(testbed.DefaultHost, testbed.GetAvailablePort(t))
	receiver := testbed.NewOTLPHTTPDataReceiver(testbed.GetAvailablePort(t))
	resourceSpec := testbed.ResourceSpec{
		ExpectedMaxCPU: 60,
		ExpectedMaxRAM: 200,
	}

	t.Run(name, func(t *testing.T) {
		testbed_tests.Scenario10kItemsPerSecond(
			t,
			sender,
			receiver,
			resourceSpec,
			performanceResultsSummary,
			nil,
			nil,
		)
	})

}
