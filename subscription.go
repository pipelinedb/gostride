package stride

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/cenkalti/backoff"

	tomb "gopkg.in/tomb.v2"
)

const delimiter = "\r\n"

// Subscription is a utility that exposes /subscribe endpoints
type Subscription struct {
	apiKey    string
	path      string
	client    *http.Client
	config    *Config
	tomb      tomb.Tomb
	connected bool
	Events    chan map[string]interface{}
}

func newSubscription(apiKey, path string, config *Config) *Subscription {
	if config == nil {
		config = defaultConfig
	}

	return &Subscription{
		apiKey,
		path,
		&http.Client{},
		config,
		tomb.Tomb{},
		false,
		make(chan map[string]interface{}),
	}
}

// Start listening for events async
func (s *Subscription) Start() {
	s.tomb.Go(s.start)
}

func (s *Subscription) start() error {
	url := fmt.Sprintf("%s%s/subscribe", s.config.Endpoint, s.path)

	lg := log.WithFields(logrus.Fields{
		"url":      url,
		"module":   "subscription",
		"function": "Start",
	})

	b := backoff.NewExponentialBackOff()
	b.InitialInterval = s.config.Subscription.InitialInterval
	b.Multiplier = 2
	b.MaxInterval = s.config.Subscription.MaxInterval
	b.Reset()

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Add("User-Agent", fmt.Sprintf("gostride (version: %s)", Version))
	req.Header.Add("Accept", "application/json")
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
			s.connected = true
			s.receive(resp.Body)
			s.connected = false
			b.Reset()
		case 429, 500, 504:
			lg.WithField("status_code", resp.StatusCode).Error("Invalid status code")
		case 404:
			return ErrResourceMissing
		default:
			return ErrServerError
		}

		wait = b.NextBackOff()
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
		"module":   "subscription",
		"function": "receive",
	})

	scanner := bufio.NewScanner(body)
	scanner.Split(scanLines)

	tokenCh := make(chan []byte)
	exited := false

	go func() {
		for scanner.Scan() {
			tokenCh <- scanner.Bytes()
		}
		// Don't log any connection errors thrown as a result of this Subscription's
		// underlying connection being purposely closed
		if !exited && scanner.Err() != nil {
			lg.WithError(scanner.Err()).Error("Error reading data")
		}
		close(tokenCh)
	}()

	written := 0
	for {
		select {
		case token, open := <-tokenCh:
			if !open {
				return
			}
			if len(token) == 0 {
				// Empty keep-alive
				continue
			}
			var event map[string]interface{}
			if err := json.Unmarshal(token, &event); err != nil {
				lg.WithError(err).Error("Failed to parse incoming event")
				continue
			}
			// Now send the event to the Subscription receiver
			select {
			case s.Events <- event:
				written++
			case <-s.tomb.Dying():
				exited = true
				return
			}
		case <-s.tomb.Dying():
			exited = true
			return
		}
	}
}

// IsConnected returns whether this Subscription is connected to its endpoint
func (s *Subscription) IsConnected() bool {
	return s.connected
}

// IsRunning returns whether the Subscription is still active
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
