package stride

import (
  "bufio"
  "bytes"
  "encoding/json"
  "errors"
  "fmt"
  "io"
  "net/http"
  "regexp"
  "strings"
  "time"

  "github.com/Sirupsen/logrus"
  "github.com/cenkalti/backoff"

  tomb "gopkg.in/tomb.v2"
)

const delimiter = "\r\n"

var validSubscriptionPaths = regexp.MustCompile(`^(collect|process)/[A-Za-z][A-Za-z0-9_]*$`)

var (
  // ErrInvalidEndpoint is returned when the subscription endpoint is not valid
  ErrInvalidEndpoint = errors.New("invalid endpoint")
)

// SubscriptionConfig is the config for a subscrption
type SubscriptionConfig struct {
  Timeout         time.Duration
  InitialInterval time.Duration
  MaxInterval     time.Duration
  Endpoint        string
}

// DefaultSubscriptionConfig is the default configuration
var DefaultSubscriptionConfig = &SubscriptionConfig{
  5 * time.Second,
  time.Second,
  300 * time.Second,
  Endpoint,
}

// Subscription is a utility that exposes /subscribe endpoints
type Subscription struct {
  apiKey string
  path   string
  client *http.Client
  config *SubscriptionConfig
  tomb   tomb.Tomb

  Events chan map[string]interface{}
}

// NewSubscription returns a new Subscription instance
func NewSubscription(apiKey, path string, config *SubscriptionConfig) (*Subscription, error) {
  path = strings.TrimRight(path, "/")
  if !validSubscriptionPaths.MatchString(path) {
    return nil, ErrInvalidEndpoint
  }

  if config == nil {
    config = DefaultSubscriptionConfig
  }

  return &Subscription{
    apiKey,
    path,
    &http.Client{
      Timeout: config.Timeout,
    },
    config,
    tomb.Tomb{},
    make(chan map[string]interface{}),
  }, nil
}

// Start listening for events async
func (s *Subscription) Start() {
  s.tomb.Go(s.start)
}

func (s *Subscription) start() error {
  url := fmt.Sprintf("%s/%s", s.config.Endpoint, s.path)

  lg := log.WithFields(logrus.Fields{
    "api_key": s.apiKey,
    "url":     url,
    "module":  "subscrption",
    "method":  "Start",
  })

  b := backoff.NewExponentialBackOff()
  b.InitialInterval = s.config.InitialInterval
  b.Multiplier = 2
  b.MaxInterval = s.config.MaxInterval
  b.Reset()

  req, _ := http.NewRequest("GET", url, nil)
  req.Header.Add("User-Agent", fmt.Sprintf("gostride (version: %s)", Version))
  req.Header.Add("Content-Type", "application/json")
  req.SetBasicAuth(s.apiKey, "")

  var wait time.Duration
  for {
    resp, err := s.client.Do(req)
    if err != nil {
      lg.WithError(err).Error("Request to Stride API failed")
      return ErrRequestFailed
    }
    defer resp.Body.Close()

    switch resp.StatusCode {
    case 200:
      s.receive(resp.Body)
      b.Reset()
    case 429, 500, 504:
      lg.WithField("status_code", resp.StatusCode).Error("Invalid status code")
      wait = b.NextBackOff()
    case 404:
      return ErrResourceMissing
    default:
      return ErrServerError
    }

    if wait == backoff.Stop {
      return ErrTimeout
    }

    resp.Body.Close()
    select {
    case <-time.After(wait):
    case <-s.tomb.Dying():
      return nil
    }
  }
}

// scanLines is a split function for a Scanner that returns each line of text
// stripped of the end-of-line marker "\r\n" used by Stride Subscription API.
func scanLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
  if atEOF && len(data) == 0 {
    return 0, nil, nil
  }

  if i := bytes.Index(data, []byte(delimiter)); i >= 0 {
    // We have a full '\r\n' terminated line.
    return i + 2, data[0:i], nil
  }

  // If we're at EOF, we have a final, non-terminated line. Return it.
  if atEOF {
    data = bytes.TrimLeft(data, "\n")
    return len(data), data, nil
  }

  // Request more data.
  return 0, nil, nil
}

func (s *Subscription) receive(body io.ReadCloser) {
  lg := log.WithFields(logrus.Fields{
    "api_key": s.apiKey,
    "module":  "subscrption",
    "method":  "receive",
  })

  scanner := bufio.NewScanner(body)
  scanner.Split(scanLines)

  for scanner.Scan() {
    select {
    case <-s.tomb.Dying():
      return
    default:
    }

    token := scanner.Bytes()
    if len(token) == 0 {
      // empty keep-alive
      continue
    }

    var event map[string]interface{}
    if err := json.Unmarshal(token, &event); err != nil {
      lg.WithError(err).Error("Failed to parse incoming event")
      continue
    }

    select {
    case s.Events <- event:
    case <-s.tomb.Dying():
      return
    }
  }

  if scanner.Err() != nil {
    lg.WithError(scanner.Err()).Error("Error reading data")
  }
}

// IsRunning returns whether the subscription is still active
func (s *Subscription) IsRunning() bool {
  return s.tomb.Alive()
}

// Stop listening for events
func (s *Subscription) Stop() error {
  s.tomb.Kill(nil)
  err := s.tomb.Wait()
  close(s.Events)

  return err
}
