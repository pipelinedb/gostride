package stride

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type SubscriptionTestSuite struct {
	suite.Suite
}

func createMockSubscribeServer(stop chan bool, delay time.Duration) (*echo.Echo, string) {
	e := echo.New()

	e.GET("v1/collect/stream/subscribe", func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderContentType, echo.MIMEApplicationJSONCharsetUTF8)
		c.Response().WriteHeader(http.StatusOK)
		c.Response().Flush()

		for {
			c.Response().Write([]byte(`{"ts": "2016-10-03T22:19:51Z", "user": "cartman"}`))
			c.Response().Write([]byte(delimiter))
			c.Response().Flush()

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

	go func() { e.Start(addr) }()
	// Wait for server to boot up
	time.Sleep(2 * time.Second)

	return e, addr
}

func (suite *SubscriptionTestSuite) TestSubscription() {
	stop := make(chan bool)
	e, addr := createMockSubscribeServer(stop, 25*time.Millisecond)

	defer func() {
		cxt, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		e.Shutdown(cxt)
	}()

	config := NewConfig()
	config.Endpoint = fmt.Sprintf("http://%s/v1", addr)

	s := newSubscription("key", "/collect/stream", config)

	s.Start()
	assert.True(suite.T(), s.IsRunning())

	var count int32
	atomic.StoreInt32(&count, 0)
	t := suite.T()
	go func() {
		for event := range s.Events {
			atomic.AddInt32(&count, 1)
			assert.Equal(t, "cartman", event["user"])
			assert.Equal(t, "2016-10-03T22:19:51Z", event["ts"])
		}
	}()

	time.Sleep(250 * time.Millisecond)

	assert.True(suite.T(), atomic.LoadInt32(&count) <= 10)
	close(stop)
	assert.True(suite.T(), atomic.LoadInt32(&count) <= 10)

	err := s.Stop()
	assert.Nil(suite.T(), err)
	assert.False(suite.T(), s.IsRunning())
}

func TestSubscriptionTestSuite(t *testing.T) {
	suite.Run(t, new(SubscriptionTestSuite))
}
