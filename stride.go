package stride

import (
	"bytes"
	"compress/gzip"
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
	// ErrInvalidPath is returned if the endpoint provided is not valid
	ErrInvalidPath = errors.New("Invalid endpoint given")
	// ErrInvalidAPIKey is returned if the api key was rejected/didn't have correct
	// permissions
	ErrInvalidAPIKey = errors.New("Invalid API key")
)

var collectPath = regexp.MustCompile(`^/collect`)
var validPaths = map[string][]*regexp.Regexp{
	http.MethodGet: {
		regexp.MustCompile(`^/(collect|process)(/[A-Za-z][A-Za-z0-9_]*)?$`),
		regexp.MustCompile(`^/process(/[A-Za-z][A-Za-z0-9_]*(/stats)?)?$`),
		regexp.MustCompile(`^/analyze(/[A-Za-z][A-Za-z0-9_]*(/results)?)?$`),
	},
	http.MethodPost: {
		regexp.MustCompile(`^/(collect|process|analyze)/[A-Za-z][A-Za-z0-9_]*$`),
		regexp.MustCompile(`^/(collect|analyze)$`),
		regexp.MustCompile(`^/analyze/[A-Za-z][A-Za-z0-9_]*/results$`),
	},
	http.MethodPut:    {regexp.MustCompile(`^/(analyze|process)/[A-Za-z][A-Za-z0-9_]*$`)},
	http.MethodDelete: {regexp.MustCompile(`^/(collect|process|analyze)/[A-Za-z][A-Za-z0-9_]*$`)},
	"Subscribe":       {regexp.MustCompile(`^/(collect|process)/[A-Za-z][A-Za-z0-9_]*$`)},
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

func compressBody(body []byte) ([]byte, error) {
	var bb bytes.Buffer
	gz := gzip.NewWriter(&bb)
	if _, err := gz.Write(body); err != nil {
		return nil, err
	}
	if err := gz.Flush(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return bb.Bytes(), nil
}

func (s *Stride) makeRequest(method, path string, data interface{}) *Response {
	if !isPathValid(method, path) {
		return &Response{
			-1,
			nil,
			ErrInvalidPath,
		}
	}

	lg := log.WithFields(logrus.Fields{
		"endpoint": s.config.Endpoint,
		"module":   "stride",
		"method":   method,
		"function": "makeRequest",
	})

	url := s.config.Endpoint + path
	var reader io.Reader
	var body []byte
	var compressed bool
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
		// Compress events written to /collect
		if collectPath.Match([]byte(path)) {
			b, err = compressBody(b)
			if err != nil {
				lg.WithError(err).Error("Failed to compress request body")
				return &Response{
					-1,
					nil,
					err,
				}
			}
			compressed = true
		}
		body = b
		reader = bytes.NewReader(b)
	}

	req, _ := http.NewRequest(method, url, reader)
	if compressed {
		req.Header.Add("Content-Encoding", "gzip")
	}
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
		return nil, ErrInvalidPath
	}

	sub := newSubscription(s.apiKey, path, s.config)
	return sub, nil
}
