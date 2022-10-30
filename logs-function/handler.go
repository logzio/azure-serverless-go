package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"time"
)

// InvokeRequest event hub trigger payload
type InvokeRequest struct {
	Data     map[string]interface{}
	Metadata map[string]interface{}
}

// InvokeResponse Function response
type InvokeResponse struct {
	Outputs     map[string]interface{}
	Logs        []string
	ReturnValue string
}
type handlerConfig struct {
	token             string
	url               string
	debug             string
	storageConnection string
}

type logzioHandler struct {
	config        handlerConfig
	httpClient    *http.Client
	httpTransport *http.Transport
	dataBuffer    bytes.Buffer
}

// initAndValidateConfig populates the handlerConfig values from environment variables
func (l *logzioHandler) initAndValidateConfig(w http.ResponseWriter) {
	debug, found := os.LookupEnv("Debug")
	if found {
		l.config.debug = debug
	}
	token, found := os.LookupEnv("LogzioToken")
	if found {
		l.config.token = token
	} else {
		http.Error(w, "Logzio token must be provided", http.StatusBadRequest)
	}
	url, found := os.LookupEnv("LogzioListener")
	if found {
		l.config.url = url
	} else {
		http.Error(w, "Logzio listener url must be provided", http.StatusBadRequest)
	}
	storageConnection, found := os.LookupEnv("LogsStorageConnectionString")
	if found {
		l.config.storageConnection = storageConnection
	} else {
		http.Error(w, "Back up storage connection string must be provided", http.StatusBadRequest)
	}
}

// export sends the data buffer bytes to logz.io
func (l *logzioHandler) export() {
	var statusCode int
	// gzip compress data before shipping
	var compressedBuf bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressedBuf)
	_, err := gzipWriter.Write(l.dataBuffer.Bytes())
	if err != nil {
		return
	}
	err = gzipWriter.Close()
	if err != nil {
		return
	}
	// retry logic
	backOff := time.Second * 2
	sendRetries := 4
	toBackOff := false
	for attempt := 0; attempt < sendRetries; attempt++ {
		if toBackOff {
			fmt.Printf("Failed to send logs, trying again in %v\n", backOff)
			time.Sleep(backOff)
			backOff *= 2
		}
		statusCode = l.makeHttpRequest(compressedBuf)
		if l.shouldRetry(statusCode) {
			toBackOff = true
		} else {
			break
		}
	}
	// Send data to back up storage in case of shipping error that cannot be resolved
	if statusCode != 200 {
		fmt.Printf("Error sending logs, status code is: %d", statusCode)
		fmt.Println("Sending logs to backup storage")
		l.sendToBackupContainer()
	}
	// reset data buffers
	l.dataBuffer.Reset()
	compressedBuf.Reset()
}

func (l *logzioHandler) makeHttpRequest(data bytes.Buffer) int {
	url := fmt.Sprintf("%s/?token=%s&type=eventhub", l.config.url, l.config.token)
	req, err := http.NewRequest("POST", url, &data)
	req.Header.Add("Content-Encoding", "gzip")
	fmt.Printf("Sending bulk of %v bytes\n", l.dataBuffer.Len())
	resp, err := l.httpClient.Do(req)
	if err != nil {
		fmt.Printf("Error sending logs to %s %s\n", url, err)
		return 400
	}
	defer resp.Body.Close()
	statusCode := resp.StatusCode
	_, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("Error reading response body: %v", err)
	}
	fmt.Printf("Response status code: %v \n", statusCode)
	return statusCode

}

// shouldRetry is responsible to decide weather to retry a http request based on the response status coed
func (l *logzioHandler) shouldRetry(statusCode int) bool {
	retry := true
	switch statusCode {
	case http.StatusBadRequest:
		fmt.Printf("Got HTTP %d bad request, skip retry\n", statusCode)
		retry = false
	case http.StatusNotFound:
		fmt.Printf("Got HTTP %d not found, skip retry\n", statusCode)
		retry = false
	case http.StatusUnauthorized:
		fmt.Printf("Got HTTP %d unauthorized, skip retry\n", statusCode)
		retry = false
	case http.StatusForbidden:
		fmt.Printf("Got HTTP %d forbidden, skip retry\n", statusCode)
		retry = false
	case http.StatusOK:
		retry = false
	}
	return retry
}

// sendToBackup sends the data buffer bytes to back container in azure storage account
func (l *logzioHandler) sendToBackupContainer() {
	ctx := context.Background()
	serviceClient, err := azblob.NewClientFromConnectionString(l.config.storageConnection, nil)
	if err != nil {
		log.Fatal("Invalid credentials with error: " + err.Error())
	}
	containerName := fmt.Sprintf("logsbackup")
	blobName := "logsbackup" + "-" + randomString()
	_, err = serviceClient.UploadBuffer(ctx, containerName, blobName, l.dataBuffer.Bytes(), nil)
	if err != nil {
		log.Fatal(err)
		return
	}

}

// writeRecordToBuffer Takes a log record compression the data and writes it to the data buffer
func (l *logzioHandler) writeRecordToBuffer(record interface{}) {
	recordBytes, marshalErr := json.Marshal(record)
	if marshalErr != nil {
		fmt.Printf("Error getting record bytes: %s", marshalErr.Error())
	}
	_, bufferErr := l.dataBuffer.Write(append(recordBytes, '\n'))
	if bufferErr != nil {
		fmt.Printf("Error writing record bytes to buffer: %s", bufferErr.Error())
	}
}

// extractLogs Takes record list from eventhub messages and converts to logs
func (l *logzioHandler) extractLogs(records []interface{}) {
	for _, record := range records {
		innerRecords := record.(map[string]interface{})["records"]
		if innerRecords != nil {
			for _, innerRecord := range innerRecords.([]interface{}) {
				l.writeRecordToBuffer(innerRecord)
			}
			continue
		}
		l.writeRecordToBuffer(record)
	}
}

// eventHubTrigger Takes eventhub messages converts to log records that are export to logz.io
func eventHubTrigger(w http.ResponseWriter, r *http.Request) {
	tlsConfig := &tls.Config{}
	transport := &http.Transport{
		Proxy:           http.ProxyFromEnvironment,
		TLSClientConfig: tlsConfig,
	}
	// in case server side is sleeping - wait 10s instead of waiting for him to wake up
	client := &http.Client{
		Transport: transport,
		Timeout:   time.Second * 10,
	}
	// Initialize handler
	logzioHandler := logzioHandler{
		httpClient:    client,
		httpTransport: transport,
		dataBuffer:    bytes.Buffer{},
	}
	logzioHandler.initAndValidateConfig(w)
	// Parsing the request
	var invokeReq InvokeRequest
	d := json.NewDecoder(r.Body)
	decodeErr := d.Decode(&invokeReq)
	if decodeErr != nil {
		http.Error(w, decodeErr.Error(), http.StatusBadRequest)
		return
	}
	if logzioHandler.config.debug == "true" {
		fmt.Printf("debug: request data: %s", invokeReq.Data["records"])
	}
	var records []interface{}
	if unmarshalErr := json.Unmarshal([]byte(invokeReq.Data["records"].(string)), &records); unmarshalErr != nil {
		http.Error(w, unmarshalErr.Error(), http.StatusInternalServerError)
	}
	logzioHandler.extractLogs(records)
	logzioHandler.export()

	outputs := make(map[string]interface{})
	outputs["statusCode"] = 200
	invokeResponse := InvokeResponse{outputs, nil, "Finished sending metrics successfully"}
	responseJson, _ := json.Marshal(invokeResponse)
	w.Header().Set("Content-Type", "application/json")
	w.Write(responseJson)
}

func main() {
	httpInvokerPort, exists := os.LookupEnv("FUNCTIONS_HTTPWORKER_PORT")
	if exists {
		fmt.Println("FUNCTIONS_HTTPWORKER_PORT: " + httpInvokerPort)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/logs-function", eventHubTrigger)
	fmt.Println("Go server Listening on httpInvokerPort:", httpInvokerPort)
	log.Fatal(http.ListenAndServe(":"+httpInvokerPort, mux))
}

func randomString() string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return strconv.Itoa(r.Int())
}
