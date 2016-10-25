package stride

import (
  "bytes"
  "encoding/json"
  "errors"
  "fmt"
  "io"
  "io/ioutil"
  "net/http"
  "regexp"
  "time"

  "github.com/Sirupsen/logrus"
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
)

var (
  // ErrRequestFailed is returned when the HTTP request could not be successfully
  // completed
  ErrRequestFailed = errors.New("Request to Stride API failed")
  // ErrServerError is returned when the server returns an unknown status code,
  // usually 500
  ErrServerError = errors.New("Stride API returned an invalid status code")
  // ErrTimeout is returned when the request times out
  ErrTimeout = errors.New("Timed out while trying to issue requests to Stride API")
  // ErrResourceMissing is returned when the Stride API 404s
  ErrResourceMissing = errors.New("No resources with the name exists")
  // ErrInvalidBody is returned if an invalid body map is provided
  ErrInvalidBody = errors.New("Invalid request body")
  // ErrInvalidResponse is returned if the response could not be read/parsed
  ErrInvalidResponse = errors.New("Invalid response body")
  // ErrInvalidEndpoint is returned if the endpoint provided is not valid
  ErrInvalidEndpoint = errors.New("Invalid endpoint given")
  // ErrInvalidAPIKey is returned if the api key was rejected/didn't have correct
  // permissions
  ErrInvalidAPIKey = errors.New("Invalid API key")
)

var validPaths = map[string][]*regexp.Regexp{
  http.MethodGet: []*regexp.Regexp{
    regexp.MustCompile(`^/(collect|process)(/[A-Za-z][A-Za-z0-9_]*)?$`),
    regexp.MustCompile(`^/analyze(/[A-Za-z][A-Za-z0-9_]*(/results)?)?$`),
  },
  http.MethodPost: []*regexp.Regexp{
    regexp.MustCompile(`^/(collect|process|analyze)/[A-Za-z][A-Za-z0-9_]*$`),
    regexp.MustCompile(`^/(collect|analyze)$`),
    regexp.MustCompile(`^/analyze/[A-Za-z][A-Za-z0-9_]*/results$`),
  },
  http.MethodPut:    []*regexp.Regexp{regexp.MustCompile(`^/analyze/[A-Za-z][A-Za-z0-9_]*$`)},
  http.MethodDelete: []*regexp.Regexp{regexp.MustCompile(`^/(collect|process|analyze)/[A-Za-z][A-Za-z0-9_]*$`)},
  "Subscribe":       []*regexp.Regexp{regexp.MustCompile(`^/(collect|process)/[A-Za-z][A-Za-z0-9_]*/subscribe$`)},
}

func isPathValid(method, path string) bool {
  for _, re := range validPaths[method] {
    if re.MatchString(path) {
      return true
    }
  }
  return false
}

func errorFromStatusCode(statusCode int) error {
  var err error

  switch statusCode {
  case http.StatusOK, http.StatusCreated:
    err = nil
  case http.StatusNotFound:
    err = ErrResourceMissing
  case http.StatusGatewayTimeout:
    err = ErrTimeout
  case http.StatusBadRequest:
    err = ErrInvalidBody
  case http.StatusUnauthorized, http.StatusForbidden:
    err = ErrInvalidAPIKey
  default:
    err = ErrServerError
  }

  return err
}

// Config is the config for the Stride API client
type Config struct {
  Timeout  time.Duration
  Endpoint string

  Subscription struct {
    InitialInterval time.Duration
    MaxInterval     time.Duration
  }
}

// defaultConfig is the default configuration
var defaultConfig = &Config{
  Timeout:  5 * time.Second,
  Endpoint: Endpoint,
  Subscription: struct {
    InitialInterval time.Duration
    MaxInterval     time.Duration
  }{
    InitialInterval: time.Second,
    MaxInterval:     300 * time.Second,
  },
}

// NewConfig returns a new default config
func NewConfig() *Config {
  c := *defaultConfig
  return &c
}

// Stride is a wrapper around the Stride API
type Stride struct {
  apiKey string
  client *http.Client
  config *Config
}

// Response is a wrapped response from the API
type Response struct {
  StatusCode int
  Data       interface{}
  Error      error
}

// NewStride returns a new Stride API client
func NewStride(apiKey string, config *Config) *Stride {
  return &Stride{
    apiKey: apiKey,
    client: &http.Client{
      Timeout: config.Timeout,
    },
    config: config,
  }
}

func (s *Stride) makeRequest(method, path string, data interface{}) *Response {
  if !isPathValid(method, path) {
    return &Response{
      -1,
      nil,
      ErrInvalidEndpoint,
    }
  }

  lg := log.WithFields(logrus.Fields{
    "api_key":  s.apiKey,
    "endpoint": s.config.Endpoint,
    "module":   "stride",
    "method":   method,
    "function": "makeRequest",
  })

  url := s.config.Endpoint + path
  var reader io.Reader
  var body []byte
  if data != nil {
    b, err := json.Marshal(data)
    if err != nil {
      lg.WithError(err).Error("Failed to JSONify request body")
      return &Response{
        -1,
        nil,
        ErrInvalidBody,
      }
    }
    body = b
    reader = bytes.NewReader(b)
  }

  req, _ := http.NewRequest(method, url, reader)
  req.Header.Add("User-Agent", fmt.Sprintf("gostride (version: %s)", Version))
  req.Header.Add("Accept", "application/json")
  req.Header.Add("Content-Type", "application/json")
  if body != nil {
    req.Header.Add("Content-Length", fmt.Sprintf("%d", len(body)))
  }
  req.SetBasicAuth(s.apiKey, "")

  res, err := s.client.Do(req)
  if err != nil {
    lg.WithError(err).Error("Request to Stride API failed")
    return &Response{
      -1,
      nil,
      ErrRequestFailed,
    }
  }
  defer res.Body.Close()

  var v interface{}

  if res.Body != nil {
    body, err = ioutil.ReadAll(res.Body)
    if err == nil && len(body) > 0 {
      err = json.Unmarshal(body, &v)
    }

    if err != nil {
      lg.WithError(err).Error("Failed to read/parse response body")

      return &Response{
        res.StatusCode,
        nil,
        ErrInvalidResponse,
      }
    }
  }

  if res.StatusCode < 200 || res.StatusCode > 201 {
    lg.WithField("status_code", res.StatusCode).Error("Stride API returned invalid status code")

    return &Response{
      res.StatusCode,
      v,
      errorFromStatusCode(res.StatusCode),
    }
  }

  return &Response{
    res.StatusCode,
    v,
    nil,
  }
}

// Get makes a GET request to the path
func (s *Stride) Get(path string) *Response {
  return s.makeRequest(http.MethodGet, path, nil)
}

// Post makes a POST request to the path
func (s *Stride) Post(path string, data interface{}) *Response {
  return s.makeRequest(http.MethodPost, path, data)
}

// Put makes a PUT request to the path
func (s *Stride) Put(path string, data interface{}) *Response {
  return s.makeRequest(http.MethodPut, path, data)
}

// Delete makes a DELETE request to the path
func (s *Stride) Delete(path string) *Response {
  return s.makeRequest(http.MethodDelete, path, nil)
}

// Subscribe makes a GET request to a subscribe endpoint
func (s *Stride) Subscribe(path string) (*Subscription, error) {
  if !isPathValid("Subscribe", path) {
    return nil, ErrInvalidEndpoint
  }

  sub := newSubscription(s.apiKey, path, s.config)
  return sub, nil
}
