package stride

import (
  "io/ioutil"
  "net/http"
  "net/http/httptest"
  "testing"

  "github.com/stretchr/testify/assert"
  "github.com/stretchr/testify/suite"
)

type StrideTestSuite struct {
  suite.Suite
}

func createMockServer(t *testing.T) *httptest.Server {
  server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    path := r.URL.Path

    w.Header().Set("Content-Type", "application/json")

    switch r.Method {
    case http.MethodGet:
      w.WriteHeader(http.StatusOK)

      if path == "/v1/collect" {
        w.Write([]byte(`["stream0", "stream1"]`))
      }
    case http.MethodPost:
      w.WriteHeader(http.StatusCreated)
      assert.NotNil(t, r.Body)
      body, _ := ioutil.ReadAll(r.Body)
      w.Write(body)
    case http.MethodDelete:
      w.WriteHeader(http.StatusOK)
    }
  }))

  return server
}

func (suite *StrideTestSuite) TestPathValidation() {
  assert.True(suite.T(), isPathValid(http.MethodGet, "/collect"))
  assert.True(suite.T(), isPathValid(http.MethodPost, "/collect"))
  assert.True(suite.T(), isPathValid(http.MethodGet, "/collect/stream"))
  assert.True(suite.T(), isPathValid(http.MethodPost, "/collect/stream"))
  assert.True(suite.T(), isPathValid(http.MethodDelete, "/collect/stream"))
  assert.True(suite.T(), isPathValid("Subscribe", "/collect/stream/subscribe"))
  assert.True(suite.T(), isPathValid(http.MethodGet, "/process"))
  assert.True(suite.T(), isPathValid(http.MethodGet, "/process/proc"))
  assert.True(suite.T(), isPathValid(http.MethodPost, "/process/proc"))
  assert.True(suite.T(), isPathValid(http.MethodDelete, "/process/proc"))
  assert.True(suite.T(), isPathValid("Subscribe", "/process/proc/subscribe"))
  assert.True(suite.T(), isPathValid(http.MethodGet, "/analyze"))
  assert.True(suite.T(), isPathValid(http.MethodPost, "/analyze"))
  assert.True(suite.T(), isPathValid(http.MethodGet, "/analyze/query"))
  assert.True(suite.T(), isPathValid(http.MethodPost, "/analyze/query"))
  assert.True(suite.T(), isPathValid(http.MethodDelete, "/analyze/query"))
  assert.True(suite.T(), isPathValid(http.MethodGet, "/analyze/query/results"))
  assert.True(suite.T(), isPathValid(http.MethodGet, "/analyze/query/results"))
  assert.True(suite.T(), isPathValid(http.MethodPut, "/analyze/query"))

  assert.False(suite.T(), isPathValid(http.MethodGet, "/collect/_stream"))
  assert.False(suite.T(), isPathValid(http.MethodGet, "/collect/1stream"))
  assert.False(suite.T(), isPathValid(http.MethodGet, "/collect/stream/results"))
  assert.False(suite.T(), isPathValid(http.MethodPost, "/process"))
  assert.False(suite.T(), isPathValid("Subscribe", "/analyze/query/subscribe"))
  assert.False(suite.T(), isPathValid(http.MethodPut, "/collect/stream"))
  assert.False(suite.T(), isPathValid(http.MethodPut, "/process/proc"))
  assert.False(suite.T(), isPathValid(http.MethodPut, "/collect"))
  assert.False(suite.T(), isPathValid(http.MethodPut, "/process"))
  assert.False(suite.T(), isPathValid(http.MethodPut, "/analyze"))
}

func (suite *StrideTestSuite) TestMethods() {
  server := createMockServer(suite.T())

  config := NewConfig()
  config.Endpoint = server.URL + "/v1"

  s := NewStride("key", config)

  r := s.Get("/collect")
  assert.Equal(suite.T(), http.StatusOK, r.StatusCode)
  assert.Nil(suite.T(), r.Error)
  streams, ok := r.Data.([]interface{})
  assert.True(suite.T(), ok)
  assert.Equal(suite.T(), []interface{}{"stream0", "stream1"}, streams)

  proc := map[string]interface{}{
    "query": "SELECT 1",
    "action": map[string]interface{}{
      "type": "MATERIALIZE",
    },
  }
  r = s.Post("/process/p1", proc)
  assert.Equal(suite.T(), http.StatusCreated, r.StatusCode)
  assert.Nil(suite.T(), r.Error)
  data, ok := r.Data.(map[string]interface{})
  assert.True(suite.T(), ok)
  assert.Equal(suite.T(), proc, data)

  r = s.Delete("/analyze/q1")
  assert.Equal(suite.T(), http.StatusOK, r.StatusCode)
  assert.Nil(suite.T(), r.Error)
  assert.Nil(suite.T(), r.Data)
}

func TestStrideTestSuite(t *testing.T) {
  suite.Run(t, new(StrideTestSuite))
}
