package main

import (
	"bytes"
	"encoding/json"
	"github.com/logzio/logzio-go"
	"log"
	"net/http"
	"os"
)

type InvokeRequest struct {
	Data     map[string]interface{}
	Metadata map[string]interface{}
}

type InvokeResponse struct {
	Outputs     map[string]interface{}
	Logs        []string
	ReturnValue string
}

type logzioHandler struct {
	sender     *logzio.LogzioSender
	dataBuffer bytes.Buffer
}

// export sends the data buffer bytes to logz.io
func (l *logzioHandler) export() {
	err := l.sender.Send(l.dataBuffer.Bytes())
	// TODO send to backup container
	if err != nil {
		log.Printf("Error sending metrics: %s", err.Error())
	}
	l.dataBuffer.Reset()
}

// writeRecordToBuffer Takes a log record and writes it to the data buffer
func (l *logzioHandler) writeRecordToBuffer(record interface{}) {
	recordBytes, marshalErr := json.Marshal(record)
	if marshalErr != nil {
		log.Printf("Error getting record bytes: %s", marshalErr.Error())
	}
	_, bufferErr := l.dataBuffer.Write(append(recordBytes, '\n'))
	if bufferErr != nil {
		log.Printf("Error writing record bytes to buffer: %s", bufferErr.Error())
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
	// Initialize logs sender
	logzioSender, err := logzio.New(
		os.Getenv("LogzioToken"),
		logzio.SetDebug(os.Stderr),
		logzio.SetInMemoryQueue(true),
		logzio.SetUrl(os.Getenv("LogzioListener")),
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
	// Initialize handler
	logzioHandler := logzioHandler{
		sender:     logzioSender,
		dataBuffer: bytes.Buffer{},
	}

	// Parsing the request
	var invokeReq InvokeRequest
	d := json.NewDecoder(r.Body)
	decodeErr := d.Decode(&invokeReq)
	if decodeErr != nil {
		http.Error(w, decodeErr.Error(), http.StatusBadRequest)
		return
	}
	if os.Getenv("Debug") == "true" {
		log.Printf("debug: request data: %s", invokeReq.Data["records"])
	}
	var records []interface{}
	if unmarshalErr := json.Unmarshal([]byte(invokeReq.Data["records"].(string)), &records); unmarshalErr != nil {
		http.Error(w, unmarshalErr.Error(), http.StatusInternalServerError)
	}
	logzioHandler.extractLogs(records)
	logzioHandler.export()
	logzioHandler.sender.Stop()

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
		log.Println("FUNCTIONS_HTTPWORKER_PORT: " + httpInvokerPort)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/logs-function", eventHubTrigger)
	log.Println("Go server Listening on httpInvokerPort:", httpInvokerPort)
	log.Fatal(http.ListenAndServe(":"+httpInvokerPort, mux))
}
