package stride

import (
  "errors"

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
)
