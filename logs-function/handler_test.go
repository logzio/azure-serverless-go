package main

import (
	"bytes"
	"encoding/json"
	"github.com/stretchr/testify/assert"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestShouldRetry(t *testing.T) {
	type getListenerUrlTest struct {
		statusCode int
		expected   bool
	}
	var getListenerUrlTests = []getListenerUrlTest{
		{400, false},
		{404, false},
		{403, false},
		{200, false},
		{401, false},
		{500, true},
		{987, true},
	}
	logzioHandler := logzioHandler{}
	for _, test := range getListenerUrlTests {
		sr := logzioHandler.shouldRetry(test.statusCode)
		assert.Equal(t, test.expected, sr)
	}
}

func TestExport(t *testing.T) {
	codes := []int{413, 400, 500, 200, 403, 404}
	for _, code := range codes {
		// Test server
		ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}))
		ts.Start()
		data := make([]byte, 100)
		logzioHandler := logzioHandler{
			config: handlerConfig{
				token: "token",
				url:   ts.URL,
			},
			httpClient: &http.Client{},
			logs:       nil,
			dataBuffer: bytes.Buffer{},
		}
		logzioHandler.dataBuffer.Write(data)
		assert.Equal(t, code, logzioHandler.export())
		assert.Equal(t, 0, logzioHandler.dataBuffer.Len())
		ts.Close()
	}
}

func TestExtractLogs(t *testing.T) {
	l := logzioHandler{
		config:     handlerConfig{},
		httpClient: &http.Client{},
		logs:       nil,
		dataBuffer: bytes.Buffer{},
	}
	data := InvokeRequest{}.Data
	jsonFile, err := os.Open("../testEvents/records.json")
	if err != nil {
		t.Fatal(err)
	}
	byteValue, _ := ioutil.ReadAll(jsonFile)
	err = json.Unmarshal(byteValue, &data)
	if err != nil {
		t.Fatal(err)
	}
	l.extractLogs(data["records"].([]interface{}))
	assert.Greater(t, l.dataBuffer.Len(), 1)
}
