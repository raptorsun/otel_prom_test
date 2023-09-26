package main

import (
	"fmt"
	"log"
	"net"
	"os"
	"path"
	"path/filepath"
	"time"

	testbed "github.com/open-telemetry/opentelemetry-collector-contrib/testbed/testbed"
)

type Scenario struct {
	name string

	// Directory where test case results and logs will be written.
	resultDir string

	// does not write out results when set to true
	skipResults bool

	// Resource spec for agent.
	resourceSpec testbed.ResourceSpec

	// Agent process. Otel Collector Runner
	agentProc testbed.OtelcolRunner
	// Agent process. Prometheus Runner
	promRunner testbed.OtelcolRunner

	Sender   testbed.DataSender
	receiver testbed.DataReceiver

	LoadGenerator *testbed.LoadGenerator
	MockBackend   *testbed.MockBackend
	validator     testbed.TestCaseValidator

	startTime time.Time

	// errorSignal indicates an error in the test case execution, e.g. process execution
	// failure or exceeding resource consumption, etc. The actual error message is already
	// logged, this is only an indicator on which you can wait to be informed.
	errorSignal chan struct{}
	// Duration is the requested duration of the tests. Configured via TESTBED_DURATION
	// env variable and defaults to 15 seconds if env variable is unspecified.
	Duration   time.Duration
	doneSignal chan struct{}
	errorCause string
}

func NewScenario(
	name string,
	dataProvider testbed.DataProvider,
	sender testbed.DataSender,
	receiver testbed.DataReceiver,
	agentProc testbed.OtelcolRunner,
	promRunner testbed.OtelcolRunner,
	validator testbed.TestCaseValidator,
	resultsSummary testbed.TestResultsSummary,
	resourceSpec testbed.ResourceSpec,
) *Scenario {
	scenario := Scenario{
		name:         name,
		errorSignal:  make(chan struct{}),
		doneSignal:   make(chan struct{}),
		startTime:    time.Now(),
		Sender:       sender,
		receiver:     receiver,
		agentProc:    agentProc,
		promRunner:   promRunner,
		resourceSpec: resourceSpec,
	}

	// Get requested test case duration from env variable.
	duration := os.Getenv("TEST_DURATION")
	if duration == "" {
		duration = "15s"
	}
	var err error
	scenario.Duration, err = time.ParseDuration(duration)
	if err != nil {
		log.Fatalf("Invalid TEST_DURATION: %v. Expecting a valid duration string.", duration)
		return nil
	}

	// Prepare directory for results.
	scenario.resultDir, err = filepath.Abs(path.Join("results", scenario.name))
	if err != nil {
		log.Fatalf("Cannot resolve %s: %s", scenario.name, err.Error())
		return nil
	}
	err = os.MkdirAll(scenario.resultDir, os.ModePerm)
	if err != nil {
		log.Fatalf("Cannot create directory %s: %s", scenario.resultDir, err.Error())
		return nil
	}

	// Set default resource check period.
	scenario.resourceSpec.ResourceCheckPeriod = 3 * time.Second
	if scenario.Duration < scenario.resourceSpec.ResourceCheckPeriod {
		// Resource check period should not be longer than entire test duration.
		scenario.resourceSpec.ResourceCheckPeriod = scenario.Duration
	}

	scenario.LoadGenerator, err = testbed.NewLoadGenerator(dataProvider, sender)
	if err != nil {
		log.Fatalf("Cannot create load generator: %s", err.Error())
		return nil
	}

	scenario.MockBackend = testbed.NewMockBackend(scenario.composeTestResultFileName("backend.log"), receiver)

	go scenario.logStats()

	return &scenario
}

func (scenario *Scenario) logStats() {
	t := time.NewTicker(scenario.resourceSpec.ResourceCheckPeriod)
	defer t.Stop()

	for {
		select {
		case <-t.C:
			scenario.logStatsOnce()
		case <-scenario.doneSignal:
			return
		}
	}
}

func (scenario *Scenario) logStatsOnce() {
	log.Printf("%s | %s | %s | %s",
		scenario.agentProc.GetResourceConsumption(),
		scenario.promRunner.GetResourceConsumption(),
		scenario.LoadGenerator.GetStats(),
		scenario.MockBackend.GetStats())
}

// Stop stops the load generator, the agent and the backend.
func (scenario *Scenario) Stop() {
	// Stop monitoring the agent
	close(scenario.doneSignal)

	// Stop all components
	scenario.StopLoad()
	scenario.StopAgent()
	scenario.StopBackend()
	scenario.StopPrometheus()

	if scenario.skipResults {
		return
	}

	// Report test results
	// scenario.validator.RecordResults(scenario)
}

func (scenario *Scenario) composeTestResultFileName(fileName string) string {
	fileName, err := filepath.Abs(path.Join(scenario.resultDir, fileName))
	if err != nil {
		log.Fatalf("Cannot resolve %s: %s", fileName, err.Error())
		return ""
	}
	return fileName
}

func (scenario *Scenario) indicateError(err error) {
	// Print to log for visibility
	log.Print(err.Error())

	scenario.errorCause = err.Error()

	// Signal the error via channel
	close(scenario.errorSignal)
}

// StartAgent starts the agent and redirects its standard output and standard error
// to "agent.log" file located in the test directory.
func (scenario *Scenario) StartAgent(args ...string) {
	logFileName := scenario.composeTestResultFileName("agent.log")

	startParams := testbed.StartParams{
		Name:        "Agent",
		LogFilePath: logFileName,
		CmdArgs:     args,
		// resourceSpec: &scenario.resourceSpec,
	}
	startParams.SetResourceSpec(&scenario.resourceSpec)

	if err := scenario.agentProc.Start(startParams); err != nil {
		scenario.indicateError(err)
		return
	}

	// Start watching resource consumption.
	go func() {
		if err := scenario.agentProc.WatchResourceConsumption(); err != nil {
			scenario.indicateError(err)
		}
	}()

	endpoint := scenario.Sender.GetEndpoint()
	if endpoint != nil {
		// Wait for agent to start. We consider the agent started when we can
		// connect to the port to which we intend to send load. We only do this
		// if the endpoint is not-empty, i.e. the sender does use network (some senders
		// like text log writers don't).
		scenario.WaitForN(func() bool {
			conn, err := net.Dial(scenario.Sender.GetEndpoint().Network(), scenario.Sender.GetEndpoint().String())
			if err == nil && conn != nil {
				conn.Close()
				return true
			}
			return false
		}, time.Second*10, fmt.Sprintf("connection to %s:%s", scenario.Sender.GetEndpoint().Network(), scenario.Sender.GetEndpoint().String()))
	}
}

// StartPrometheus starts the agent and redirects its standard output and standard error
// to "agent.log" file located in the test directory.
func (scenario *Scenario) StartPrometheus(args ...string) {
	logFileName := scenario.composeTestResultFileName("prometheus.log")

	startParams := testbed.StartParams{
		Name:        "Prometheus",
		LogFilePath: logFileName,
		CmdArgs:     args,
		// resourceSpec: &scenario.resourceSpec,
	}
	startParams.SetResourceSpec(&scenario.resourceSpec)

	if err := scenario.promRunner.Start(startParams); err != nil {
		scenario.indicateError(err)
		return
	}

	// Start watching resource consumption.
	go func() {
		if err := scenario.promRunner.WatchResourceConsumption(); err != nil {
			scenario.indicateError(err)
		}
	}()

	// endpoint := scenario.Sender.GetEndpoint()
	endpoint, err := net.ResolveTCPAddr("tcp", fmt.Sprintf("127.0.0.1:%d", PortPrometheus))
	if err != nil {
		scenario.indicateError(err)
		return
	}

	if endpoint != nil {
		// Wait for Prometheus to start. We consider the Prometheus started when we can
		// connect to the port to which we intend to send load. We only do this
		// if the endpoint is not-empty, i.e. the sender does use network (some senders
		// like text log writers don't).
		scenario.WaitForN(func() bool {
			conn, err := net.Dial(endpoint.Network(), endpoint.String())
			if err == nil && conn != nil {
				conn.Close()
				return true
			}
			return false
		}, time.Second*10, fmt.Sprintf("connection to %s:%s", endpoint.Network(), endpoint.String()))
	}
}

// StopAgent stops agent process.
func (scenario *Scenario) StopAgent() {
	if _, err := scenario.agentProc.Stop(); err != nil {
		scenario.indicateError(err)
	}
}

// StopPrometheus stops prometheus process.
func (scenario *Scenario) StopPrometheus() {
	if _, err := scenario.promRunner.Stop(); err != nil {
		scenario.indicateError(err)
	}
}

// StartLoad starts the load generator and redirects its standard output and standard error
// to "load-generator.log" file located in the test directory.
func (scenario *Scenario) StartLoad(options testbed.LoadOptions) {
	scenario.LoadGenerator.Start(options)
}

// StopLoad stops load generator.
func (scenario *Scenario) StopLoad() {
	scenario.LoadGenerator.Stop()
}

// StartBackend starts the specified backend type.
func (scenario *Scenario) StartBackend() {
	scenario.MockBackend.EnableRecording()
	err := scenario.MockBackend.Start()
	if err != nil {
		log.Fatalf("Cannot start backend: %s", err.Error())
	}
}

// StopBackend stops the backend.
func (scenario *Scenario) StopBackend() {
	scenario.MockBackend.Stop()
}

// WaitForN the specific condition for up to a specified duration. Records a test error
// if time is out and condition does not become true. If error is signaled
// while waiting the function will return false, but will not record additional
// test error (we assume that signaled error is already recorded in indicateError()).
func (scenario *Scenario) WaitForN(cond func() bool, duration time.Duration, errMsg interface{}) bool {
	startTime := time.Now()

	// Start with 5 ms waiting interval between condition re-evaluation.
	waitInterval := time.Millisecond * 5

	for {
		if cond() {
			return true
		}

		select {
		case <-time.After(waitInterval):
		case <-scenario.errorSignal:
			return false
		}

		// Increase waiting interval exponentially up to 500 ms.
		if waitInterval < time.Millisecond*500 {
			waitInterval *= 2
		}

		if time.Since(startTime) > duration {
			// Waited too long
			log.Fatalf("Time out waiting for %s", errMsg)
			return false
		}
	}
}

// Sleep for specified duration or until error is signaled.
func (scenario *Scenario) Sleep(d time.Duration) {
	select {
	case <-time.After(d):
	case <-scenario.errorSignal:
	}
}
