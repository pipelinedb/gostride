package stride

import (
  "bytes"
  "encoding/json"
  "errors"
  "fmt"
  "net/http"
  "sync"
  "time"

  "github.com/Sirupsen/logrus"
  tomb "gopkg.in/tomb.v2"
)

var log = logrus.New()

const (
  // Version of client library
  Version = "0.1"
  // Endpoint for Stride API
  Endpoint = "https://api.stride.com/v1"

  // Timestamp is the key to be used for the event timestamp
  Timestamp = "$timestamp"
  // ID is the key to be used for the event ID
  ID = "$id"

  maxReqsInFlight = 1000
)

// SetTimestamp sets the timestamp of an event
func SetTimestamp(event map[string]interface{}, ts time.Time) {
  event[Timestamp] = ts.Format(time.RFC3339Nano)
}

// SetID sets the ID of an event
func SetID(event map[string]interface{}, id string) {
  event[ID] = id
}

// Config is the configuration for Stride collector/client
type Config struct {
  FlushInterval time.Duration
  BatchSize     int
  Timeout       time.Duration
  Endpoint      string
  Debug         bool
}

// DefaultConfig is the default configuration
var DefaultConfig = &Config{
  250 * time.Millisecond,
  1000,
  5 * time.Second,
  Endpoint,
  false,
}

var (
  // ErrRequestFailed is returned when the HTTP request could not be successfully
  // completed
  ErrRequestFailed = errors.New("Request to Stride API failed")
  // ErrServerError is returned when the server returns an unknown status code,
  // usually 500
  ErrServerError = errors.New("Stride API returned an invalid status code")
)

type collectRequest struct {
  stream string
  events []map[string]interface{}
}

// Collector is an asynchronous client to the Stride API's collect endpoint.
type Collector struct {
  apiKey string

  // config
  config *Config

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
func NewCollector(apiKey string, config *Config) *Collector {
  if config == nil {
    config = DefaultConfig
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
    "api_key":  c.apiKey,
    "endpoint": c.config.Endpoint,
    "module":   "collector",
    "method":   "makeRequest",
  })

  b, _ := json.Marshal(events)

  url := c.config.Endpoint + "/collect"
  req, err := http.NewRequest("POST", url, bytes.NewReader(b))
  if err != nil {
    lg.WithError(err).Error("Request to Stride API Failed")
    return ErrRequestFailed
  }

  req.Header.Add("User-Agent", fmt.Sprintf("gostride (version: %s)", Version))
  req.Header.Add("Content-Type", "application/json")
  req.Header.Add("Content-Length", fmt.Sprintf("%d", len(b)))
  req.SetBasicAuth(c.apiKey, "")

  res, err := c.client.Do(req)
  if err != nil {
    lg.WithError(err).Error("Request to Stride API Failed")
    return ErrRequestFailed
  }
  defer res.Body.Close()

  if res.StatusCode == 200 {
    return nil
  }

  lg.WithField("status_code", res.StatusCode).Error("Stride API returned invalid status code")
  return ErrServerError
}

func (c *Collector) start() error {
  lg := log.WithFields(logrus.Fields{
    "api_key":  c.apiKey,
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
