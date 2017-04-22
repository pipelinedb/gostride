package stride

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/Sirupsen/logrus"
	tomb "gopkg.in/tomb.v2"
)

const maxReqsInFlight = 1000

// SetTimestamp sets the timestamp of an event
func SetTimestamp(event map[string]interface{}, ts time.Time) {
	event[Timestamp] = ts.Format(time.RFC3339Nano)
}

// SetID sets the ID of an event
func SetID(event map[string]interface{}, id string) {
	event[ID] = id
}

// CollectorConfig is the configuration for Stride collector
type CollectorConfig struct {
	FlushInterval time.Duration
	BatchSize     int
	Timeout       time.Duration
	Endpoint      string
	Debug         bool
}

// defaultCollectorConfig is the default configuration
var defaultCollectorConfig = &CollectorConfig{
	250 * time.Millisecond,
	1000,
	5 * time.Second,
	Endpoint,
	false,
}

// NewCollectorConfig returns a new default collector config
func NewCollectorConfig() *CollectorConfig {
	c := *defaultCollectorConfig
	return &c
}

type collectRequest struct {
	stream string
	events []map[string]interface{}
}

// Collector is an asynchronous client to the Stride API's collect endpoint.
type Collector struct {
	apiKey string

	// config
	config *CollectorConfig

	client   *http.Client
	incoming chan collectRequest

	// Synchronization for ensuring we don't have more than `maxReqsInFlight`
	// concurrent async collect requests
	wg        sync.WaitGroup
	semaphone chan bool

	// Go-routine lifecycle
	tomb tomb.Tomb
}

// NewCollector returns a new collector
func NewCollector(apiKey string, config *CollectorConfig) *Collector {
	if config == nil {
		config = defaultCollectorConfig
	}

	c := &Collector{
		apiKey: apiKey,
		config: config,
		client: &http.Client{
			Timeout: config.Timeout,
		},
		incoming:  make(chan collectRequest, 100),
		semaphone: make(chan bool, maxReqsInFlight),
	}

	if c.config.Debug {
		log.Level = logrus.DebugLevel
	}

	// Start the goroutine that issues async requests to Stride API
	c.tomb.Go(c.start)

	return c
}

func (c *Collector) makeRequest(events map[string][]map[string]interface{}) error {
	lg := log.WithFields(logrus.Fields{
		"endpoint": c.config.Endpoint,
		"module":   "collector",
		"function": "makeRequest",
	})

	b, err := json.Marshal(events)
	if err != nil {
		lg.WithError(err).Error("Failed to JSONify request body")
		return ErrInvalidBody
	}

	url := c.config.Endpoint + "/collect"
	req, _ := http.NewRequest("POST", url, bytes.NewReader(b))

	req.Header.Add("User-Agent", fmt.Sprintf("gostride (version: %s)", Version))
	req.Header.Add("Accept", "application/json")
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Content-Length", fmt.Sprintf("%d", len(b)))
	req.SetBasicAuth(c.apiKey, "")

	res, err := c.client.Do(req)
	if err != nil {
		lg.WithError(err).Error("Request to Stride API failed")
		return ErrRequestFailed
	}
	defer res.Body.Close()

	if res.StatusCode == 200 {
		return nil
	}

	lg.WithField("status_code", res.StatusCode).Error("Stride API returned invalid status code")
	return errorFromStatusCode(res.StatusCode)
}

func (c *Collector) start() error {
	lg := log.WithFields(logrus.Fields{
		"endpoint": c.config.Endpoint,
		"module":   "collector",
	})

	tick := time.NewTicker(c.config.FlushInterval)
	events := make(map[string][]map[string]interface{})
	numBuffered := 0

	lg.Debug("Starting collector...")

	flushEvents := func() {
		c.semaphone <- true
		c.wg.Add(1)

		lg.WithFields(logrus.Fields{
			"num_events":  numBuffered,
			"num_streams": len(events),
		}).Debug("Flushing events to server")

		go func(events map[string][]map[string]interface{}) {
			c.makeRequest(events)
			c.wg.Done()
			<-c.semaphone
		}(events)

		// Reset
		events = make(map[string][]map[string]interface{})
		numBuffered = 0
	}

	for {
		select {
		case req, ok := <-c.incoming:
			if !ok {
				break
			}

			events[req.stream] = append(events[req.stream], req.events...)
			numBuffered += len(req.events)
			if numBuffered >= c.config.BatchSize {
				flushEvents()
			}

			lg.WithFields(logrus.Fields{
				"num_events": len(req.events),
				"stream":     req.stream,
			}).Debug("Received new events")
		case <-tick.C:
			// Flush interval elapsed?
			if numBuffered > 0 {
				flushEvents()
			}
		case <-c.tomb.Dying():
			tick.Stop()

			lg.Debug("Shutting down collector...")

			// Drain any remaining messages
			for req := range c.incoming {
				events[req.stream] = append(events[req.stream], req.events...)
				numBuffered += len(req.events)
			}

			if numBuffered > 0 {
				flushEvents()
			}

			// Wait for all HTTP requests to finish
			c.wg.Wait()

			return nil
		}
	}
}

// Close shuts down the collector
func (c *Collector) Close() {
	c.tomb.Kill(nil)
	close(c.incoming)
	c.tomb.Wait()
}

// Collect collects events into a stream
func (c *Collector) Collect(stream string, events ...map[string]interface{}) {
	c.incoming <- collectRequest{stream, events}
}
