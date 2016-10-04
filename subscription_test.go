package stride

import (
  "fmt"
  "net"
  "net/http"
  "testing"
  "time"

  "github.com/labstack/echo"
  "github.com/labstack/echo/engine/standard"
  "github.com/stretchr/testify/assert"
  "github.com/stretchr/testify/suite"
)

type SubscriptionTestSuite struct {
  suite.Suite
}

func createMockSubscribeServer(stop chan bool, delay time.Duration) (*echo.Echo, string) {
  e := echo.New()

  e.GET("/collect/stream", func(c echo.Context) error {
    c.Response().Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSONCharsetUTF8)
    c.Response().WriteHeader(http.StatusOK)

    for {
      c.Response().Write([]byte(`{"ts": "2016-10-03T22:19:51Z", "user": "cartman"}`))
      c.Response().Write([]byte(delimiter))
      c.Response().(http.Flusher).Flush()

      select {
      case <-stop:
        return nil
      case <-time.After(delay):
      }
    }

  })

  l, _ := net.Listen("tcp", "localhost:0")
  addr := l.Addr().String()
  l.Close()

  go func() { e.Run(standard.New(addr)) }()
  // Wait for server to boot up
  time.Sleep(2 * time.Second)

  return e, addr
}

func (suite *SubscriptionTestSuite) TestSubscription() {
  stop := make(chan bool)
  e, addr := createMockSubscribeServer(stop, 25*time.Millisecond)
  defer e.Stop()

  config := &SubscriptionConfig{
    5 * time.Second,
    time.Second,
    300 * time.Second,
    fmt.Sprintf("http://%s", addr),
  }

  s, err := NewSubscription("key", "collect/stream", config)
  assert.Nil(suite.T(), err)

  s.Start()
  assert.True(suite.T(), s.IsRunning())

  count := 0
  go func() {
    for e := range s.Events {
      count++
      assert.Equal(suite.T(), "cartman", e["user"])
      assert.Equal(suite.T(), "2016-10-03T22:19:51Z", e["ts"])
    }
  }()

  time.Sleep(250 * time.Millisecond)

  assert.True(suite.T(), count <= 10)
  close(stop)
  assert.True(suite.T(), count <= 10)

  err = s.Stop()
  assert.Nil(suite.T(), err)
  assert.False(suite.T(), s.IsRunning())
}

func TestSubscriptionTestSuite(t *testing.T) {
  suite.Run(t, new(SubscriptionTestSuite))
}
