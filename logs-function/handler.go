package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"golang.org/x/exp/slices"
	"io/ioutil"
	"log"
	"math/rand"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"time"
)

const maxBulkSize = 10000000

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
	config     handlerConfig
	httpClient *http.Client
	logs       []string
	dataBuffer bytes.Buffer
}

// initAndValidateConfig populates the handlerConfig values from environment variables
func (l *logzioHandler) initAndValidateConfig() error {
	debug, found := os.LookupEnv("Debug")
	if found {
		l.config.debug = debug
	}
	token, found := os.LookupEnv("LogzioToken")
	if found {
		match, _ := regexp.MatchString("[a-zA-Z]{32}", token)
		if match {
			l.config.token = token
		} else {
			return errors.New("logzio token is not valid")
		}
	} else {
		return errors.New("logzio token must be provided")
	}
	url, found := os.LookupEnv("LogzioListener")
	if found {
		validListenerAddresses := []string{"https://listener.logz.io:8071", "https://listener-au.logz.io:8071", "https://listener-wa.logz.io:8071", "https://listener-nl.logz.io:8071", "https://listener-ca.logz.io:8071", "https://listener-eu.logz.io:8071", "https://listener-uk.logz.io:8071"}
		if slices.Contains(validListenerAddresses, url) {
			l.config.url = url
		} else {
			return errors.New("logzio listener url is not valid")
		}
	} else {
		return errors.New("logzio listener url must be provided")
	}
	storageConnection, found := os.LookupEnv("LogsStorageConnectionString")
	if found && storageConnection != "" {
		l.config.storageConnection = storageConnection
	} else {
		return errors.New("back up storage connection string must be provided")
	}
	return nil
}

// export sends the data buffer bytes to logz.io
func (l *logzioHandler) export() int {
	var statusCode int
	// gzip compress data before shipping
	var compressedBuf bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressedBuf)
	_, err := gzipWriter.Write(l.dataBuffer.Bytes())
	if err != nil {
		l.dataBuffer.Reset()
		compressedBuf.Reset()
		return http.StatusInternalServerError
	}
	// listener limitation 10mb
	if compressedBuf.Len() > maxBulkSize {
		l.logs = append(l.logs, fmt.Sprintf("Bulk size is larger than %d bytes, cancelling export", maxBulkSize))
		l.dataBuffer.Reset()
		compressedBuf.Reset()
		return http.StatusRequestEntityTooLarge
	}
	err = gzipWriter.Close()
	if err != nil {
		l.dataBuffer.Reset()
		compressedBuf.Reset()
		return http.StatusInternalServerError
	}
	// retry logic
	backOff := time.Second * 2
	sendRetries := 4
	toBackOff := false
	for attempt := 0; attempt < sendRetries; attempt++ {
		if toBackOff {
			l.logs = append(l.logs, fmt.Sprintf("Failed to send logs, trying again in %v\n", backOff))
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
		l.logs = append(l.logs, fmt.Sprintf("Error sending logs, status code is: %d", statusCode))
		l.logs = append(l.logs, "Sending logs to backup storage")
		l.sendToBackupContainer()
	}
	// reset data buffers
	l.dataBuffer.Reset()
	compressedBuf.Reset()
	return statusCode
}

func (l *logzioHandler) makeHttpRequest(data bytes.Buffer) int {
	url := fmt.Sprintf("%s/?token=%s&type=eventhub", l.config.url, l.config.token)
	req, err := http.NewRequest("POST", url, &data)
	req.Header.Add("Content-Encoding", "gzip")
	l.logs = append(l.logs, fmt.Sprintf("Sending bulk of %v bytes\n", l.dataBuffer.Len()))
	resp, err := l.httpClient.Do(req)
	if err != nil {
		l.logs = append(l.logs, fmt.Sprintf("Error sending logs to %s %s\n", url, err))
		return resp.StatusCode
	}
	defer resp.Body.Close()
	statusCode := resp.StatusCode
	_, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		l.logs = append(l.logs, fmt.Sprintf("Error reading response body: %v", err))
	}
	l.logs = append(l.logs, fmt.Sprintf("Response status code: %v \n", statusCode))
	return statusCode
}

// shouldRetry is responsible to decide weather to retry a http request based on the response status coed
func (l *logzioHandler) shouldRetry(statusCode int) bool {
	retry := true
	switch statusCode {
	case http.StatusBadRequest:
		l.logs = append(l.logs, fmt.Sprintf("Got HTTP %d bad request, skip retry\n", statusCode))
		retry = false
	case http.StatusNotFound:
		l.logs = append(l.logs, fmt.Sprintf("Got HTTP %d not found, skip retry\n", statusCode))
		retry = false
	case http.StatusUnauthorized:
		l.logs = append(l.logs, fmt.Sprintf("Got HTTP %d unauthorized, check your logzio shipping token\n", statusCode))
		retry = false
	case http.StatusForbidden:
		l.logs = append(l.logs, fmt.Sprintf("Got HTTP %d forbidden, skip retry\n", statusCode))
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
		l.logs = append(l.logs, fmt.Sprintf("Invalid credentials with error: "+err.Error()))
		return
	}
	containerName := fmt.Sprintf("logsbackup")
	blobName := "logsbackup" + "-" + randomString()
	_, err = serviceClient.UploadBuffer(ctx, containerName, blobName, l.dataBuffer.Bytes(), nil)
	if err != nil {
		fmt.Print(err)
		return
	}

}

// writeRecordToBuffer Takes a log record compression the data and writes it to the data buffer
func (l *logzioHandler) writeRecordToBuffer(record interface{}) error {
	recordBytes, marshalErr := json.Marshal(record)
	if marshalErr != nil {
		return errors.New(fmt.Sprintf("Error getting record bytes: %s", marshalErr.Error()))
	}
	_, bufferErr := l.dataBuffer.Write(append(recordBytes, '\n'))
	if bufferErr != nil {
		return errors.New(fmt.Sprintf("Error writing record bytes to buffer: %s", bufferErr.Error()))
	}
	return nil
}

// extractLogs Takes record list from eventhub messages and converts to logs
func (l *logzioHandler) extractLogs(records []interface{}) {
	for _, record := range records {
		innerRecords := record.(map[string]interface{})["records"]
		if innerRecords != nil {
			for _, innerRecord := range innerRecords.([]interface{}) {
				writeError := l.writeRecordToBuffer(innerRecord)
				if writeError != nil {
					l.logs = append(l.logs, writeError.Error())
				}
			}
			continue
		}
		writeError := l.writeRecordToBuffer(record)
		if writeError != nil {
			l.logs = append(l.logs, writeError.Error())
		}
	}
}

// eventHubTrigger Takes eventhub messages converts to log records that are export to logz.io
func eventHubTrigger(w http.ResponseWriter, r *http.Request) {
	// in case server side is sleeping - wait 10s instead of waiting for him to wake up
	client := &http.Client{
		Timeout: time.Second * 10,
	}
	// Initialize handler
	logzioHandler := logzioHandler{
		httpClient: client,
		dataBuffer: bytes.Buffer{},
		logs:       []string{},
	}
	configError := logzioHandler.initAndValidateConfig()
	if configError != nil {
		http.Error(w, configError.Error(), http.StatusBadRequest)
	}
	// Parsing the request
	var invokeReq InvokeRequest
	d := json.NewDecoder(r.Body)
	decodeErr := d.Decode(&invokeReq)

	if decodeErr != nil {
		http.Error(w, decodeErr.Error(), http.StatusBadRequest)
		return
	}
	if logzioHandler.config.debug == "true" {
		logzioHandler.logs = append(logzioHandler.logs, fmt.Sprintf("debug: request data: %s", invokeReq.Data["records"]))
	}
	var records []interface{}
	if unmarshalErr := json.Unmarshal([]byte(invokeReq.Data["records"].(string)), &records); unmarshalErr != nil {
		http.Error(w, unmarshalErr.Error(), http.StatusInternalServerError)
		return
	}
	logzioHandler.extractLogs(records)
	exportStatusCode := logzioHandler.export()
	if exportStatusCode != 200 {
		http.Error(w, errors.New("error while exporting logs to logz.io").Error(), exportStatusCode)
	}
	outputs := make(map[string]interface{})
	outputs["statusCode"] = 200
	invokeResponse := InvokeResponse{outputs, logzioHandler.logs, "Finished sending logs successfully"}
	responseJson, _ := json.Marshal(invokeResponse)
	w.Header().Set("Content-Type", "application/json")
	w.Write(responseJson)

}

func main() {
	httpInvokerPort, exists := os.LookupEnv("FUNCTIONS_HTTPWORKER_PORT")
	if exists {
		fmt.Printf("FUNCTIONS_HTTPWORKER_PORT: " + httpInvokerPort)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/logs-function", eventHubTrigger)
	log.Fatal(http.ListenAndServe(":"+httpInvokerPort, mux))
}

func randomString() string {
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	return strconv.Itoa(r.Int())
}
