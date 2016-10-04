package stride

import (
  "encoding/json"
  "fmt"
  "io/ioutil"
  "net/http"
  "net/http/httptest"
  "testing"
  "time"

  "github.com/stretchr/testify/assert"
  "github.com/stretchr/testify/suite"
)

type CollectorTestSuite struct {
  suite.Suite
}

type mockRequest struct {
  time time.Time
  body map[string]interface{}
}

func createMockCollectServer() (*httptest.Server, chan mockRequest) {
  rchan := make(chan mockRequest, 1)

  server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    body, _ := ioutil.ReadAll(r.Body)

    var v map[string]interface{}
    json.Unmarshal(body, &v)

    rchan <- mockRequest{time.Now(), v}
  }))

  return server, rchan
}

func (suite *CollectorTestSuite) TestEventFuncs() {
  event := make(map[string]interface{})
  location, _ := time.LoadLocation("America/New_York")

  SetID(event, "bojack")
  SetTimestamp(event, time.Date(2016, time.September, 10, 57, 0, 0, 0, location))

  assert.Equal(suite.T(), "bojack", event[ID])
  assert.Equal(suite.T(), "2016-09-12T09:00:00-04:00", event[Timestamp])
}

func (suite *CollectorTestSuite) TestCollector() {
  server, rchan := createMockCollectServer()
  defer server.Close()

  config := &CollectorConfig{
    FlushInterval: 250 * time.Millisecond,
    BatchSize:     10,
    Endpoint:      server.URL,
    Debug:         false,
  }

  collector := NewCollector("deadbeef", config)
  defer collector.Close()

  event := map[string]interface{}{
    "name":     "BoJack Horseman",
    "location": "The Internets",
  }

  // Test for FlushInterval
  startTime := time.Now()
  collector.Collect("s0", event)
  request := <-rchan

  assert.True(suite.T(), time.Now().Sub(startTime) > config.FlushInterval)
  assert.Equal(suite.T(), map[string]interface{}{
    "s0": []interface{}{event},
  }, request.body)

  // Test for BatchSize and multiple streams
  for i := 0; i < 20; i++ {
    collector.Collect(fmt.Sprintf("s%d", i%2), event)
  }

  for i := 0; i < 2; i++ {
    request = <-rchan
    assert.Equal(suite.T(), map[string]interface{}{
      "s0": []interface{}{event, event, event, event, event},
      "s1": []interface{}{event, event, event, event, event},
    }, request.body)
  }

  // Test for multiple events
  collector.Collect("s0", event, event, event)
  request = <-rchan
  assert.Equal(suite.T(), map[string]interface{}{
    "s0": []interface{}{event, event, event},
  }, request.body)
}

func TestCollectorTestSuite(t *testing.T) {
  suite.Run(t, new(CollectorTestSuite))
}
