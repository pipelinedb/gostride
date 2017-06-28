# gostride

[![CircleCI](https://circleci.com/gh/pipelinedb/gostride.svg?style=shield)](https://circleci.com/gh/pipelinedb/gostride)
[![Go Report Card](https://goreportcard.com/badge/pipelinedb/gostride)](https://goreportcard.com/report/pipelinedb/gostride) 

Go client library for [Stride](https://www.stride.io/docs)

## Install

To begin using `gostride` within your Go code, install it with:

```
go get github.com/pipelinedb/gostride
```

## Stride

To use `gostride` in your Go project, create a new instance of the `Stride` type, passing it your API key and a `Config`:

```go

conf := NewConfig()
stride := NewStride("your_api_key", conf)

`Stride` is a thin wrapper around [Stride's HTTP API](https://www.stride.io/docs), so there are only a few main methods
to use: `Get`, `Post`, `Put`, `Delete`, and `Subscribe`. All methods except for `Subscribe` return an instance of `Response`,
which has three important members:

* `StatusCode` - The HTTP status code of the server's response
* `Data` - JSON-encoded `interface{}` containing response data
* `Error` - The `error` occurred during the request, if any

### Get(path string)

* `path` - url to `GET` from

```go
// Get a list of all streams
response := stride.Get("/collect")

streams := response.Data.([]interface{})

for _, s := range streams {
  fmt.Println(s)
}
```

### Post(path string, data interface{})

* `path` - url to `POST` data to
* `data` - JSON-serialiable request body

```go

// Create a simple MATERIALIZE process
response := stride.Post("/process/simple", map[string]interface{}{
  "query": "SELECT count(*) FROM some_stream",
  "action": "MATERIALIZE",
})

// Which returns the process we just created
proc := response.Data.(map[string]interface{})
fmt.Println(proc["name"])
```

### Put(path string, data interface{})

* `path` - url to `PUT` data at
* `data` - JSON-serialiable request body

```go

// Update one of our saved queries
stride.Put("/analyze/saved_query", map[string]interface{}{
  "query": "SELECT sum(value) FROM materialize_proc",
})
```

### Delete(path string)

* `path` - url to `DELETE` from

```go

// Delete a saved query
stride.Delete("/analye/saved_query")

```

### Subscribe(path string)

`Subscribe` is slightly different from the other methods, because it doesn't map directly to a traditional HTTP request type. `Subscribe`
opens a long-lived HTTP connection and continuously receives events from the server (see [the API docs](https://www.stride.io/docs) for more
information about `/subscribe` endpoints).

`Subscribe` returns an instance of a `Subscription`, which must be explicitly started to begin receiving events. Once a `Subscription` is running, 
it will begin receiving events over its `Events` channel:

```go

// Let's subscribe to a stream of changes made to one of our MATERIALIZE processes
subscription := stride.Subscribe("/process/simple")
subscription.Start()

// Since we're subscribed to a MATERIALIZE process, our events will contain old and new rows representing an incremental update
for event := range s.Events {
  fmt.Printf("count changed from %d to %d\n", event["old"]["count"], event["new"]["count"])
}

// Remember to clean up
subscription.Stop()
```

Remember to close your `Subscription` connections with `Stop` when you're done with them, otherwise they'll accumulate on the server
and will eventually prevent you from opening new ones.

