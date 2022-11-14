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
func TestInitAndValidateConfig(t *testing.T) {
	type InitAndValidateConfigTest struct {
		token       string
		url         string
		connection  string
		expectedErr bool
	}
	var InitAndValidateConfigTests = []InitAndValidateConfigTest{
		{"fakeYdhCHUJPlBVYZSBncSMABogmtoken", "https://listener.logz.io:8071", "connection", false},
		{"badformat", "https://listener.logz.io:8071", "connection", true},
		{"fakeYdhCHUJPlBVYZSBncSMABogmtoken", "https://notvalid.logz.io:8071", "connection", true},
		{"fakeYdhCHUJPlBVYZSBncSMABogmtoken", "https://listener.logz.io:8071", "", true},
	}
	logzioHandler := logzioHandler{}
	for _, test := range InitAndValidateConfigTests {
		err := os.Setenv("LogsStorageConnectionString", test.connection)
		if err != nil {
			t.Fatal(err.Error())
		}
		err = os.Setenv("LogzioListener", test.url)
		if err != nil {
			t.Fatal(err.Error())
		}
		err = os.Setenv("LogzioToken", test.token)
		if err != nil {
			t.Fatal(err.Error())
		}
		if test.expectedErr {
			cError := logzioHandler.initAndValidateConfig()
			assert.Error(t, cError)
		} else {
			cError := logzioHandler.initAndValidateConfig()
			assert.NoError(t, cError)
		}
	}
	t.Cleanup(func() {
		err := os.Unsetenv("LogsStorageConnectionString")
		if err != nil {
			panic(err)
		}
		err = os.Unsetenv("LogzioToken")
		if err != nil {
			panic(err)
		}
		err = os.Unsetenv("LogzioListener")
		if err != nil {
			panic(err)
		}
	})
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

func TestWriteRecordToBuffer(t *testing.T) {
	type WriteRecordToBufferTest struct {
		input         interface{}
		errorExpected bool
	}
	var WriteRecordToBufferTests = []WriteRecordToBufferTest{
		{nil, false},
		{[]byte{97, 98, 99, 100, 101, 102}, false},
		{"string", false},
		{make(chan int), true},
	}
	logzioHandler := logzioHandler{}
	for _, test := range WriteRecordToBufferTests {
		if test.errorExpected {
			assert.Error(t, logzioHandler.writeRecordToBuffer(test.input))
		} else {
			assert.NoError(t, logzioHandler.writeRecordToBuffer(test.input))
		}
	}
}

//func TestEventhubTrigger(t *testing.T) {
//	err := os.Setenv("LogzioToken", "fakeYdhCHUJPlBVYZSBncSMABogmtoken")
//	if err != nil {
//		t.Fatal(err.Error())
//	}
//	err = os.Setenv("LogzioListener", "https://listener.logz.io:8071")
//	if err != nil {
//		t.Fatal(err.Error())
//	}
//	err = os.Setenv("LogsStorageConnectionString", "connectionString")
//	if err != nil {
//		t.Fatal(err.Error())
//	}
//	jsonFile, err := os.Open("../testEvents/records.json")
//	if err != nil {
//		t.Fatal(err)
//	}
//	byteValue, _ := ioutil.ReadAll(jsonFile)
//	reader := bytes.NewReader(byteValue)
//	body := io.NopCloser(reader)
//	var request = http.Request{
//		Body: body,
//	}
//	var w http.ResponseWriter
//	eventHubTrigger(w, &request)
//
//}

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
